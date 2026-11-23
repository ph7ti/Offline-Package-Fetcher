package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opf/internal/core"
)

const defaultWarmPreviewTTL = 15 * time.Minute

// PackagePreview is the pre-pipeline package inventory/diff result. It is
// created before downloading any package and is persisted in the session so
// aborted decisions are still auditable in the report.
type PackagePreview struct {
	SessionID int64
	Data      core.PackagePreviewData
	Upgrades  map[string]bool
}

// PreviewPackageDiff resolves candidate repository versions and compares them
// with the packages installed on the SSH destination. It does not download
// package artifacts. The caller must call AbortPreview or RunPreviewed.
func (s *Service) PreviewPackageDiff(ctx context.Context) (*PackagePreview, error) {
	mode := s.cfg.VersionReport
	if mode == "" {
		mode = core.VersionReportDefault
	}
	if mode == core.VersionReportNone {
		return nil, nil
	}
	if s.cfg.SSH.Host == "" {
		return nil, fmt.Errorf("coleta de versões exige SSH configurado")
	}
	if s.inspect == nil || s.transfer == nil {
		return nil, fmt.Errorf("inspetor SSH não configurado")
	}
	if _, ok := s.fetcher.(core.PackageVersionResolver); !ok {
		return nil, fmt.Errorf("fetcher não suporta resolução de versões candidatas")
	}

	session := s.newSession()
	session.IniciadaEm = time.Now().UTC()
	session.Status = core.SessaoEmAndamento
	session.EtapaAtual = 1
	if err := s.repo.CreateSessao(ctx, session); err != nil {
		return nil, fmt.Errorf("criar sessão de pré-diff: %w", err)
	}
	preparedHere := false
	connectedHere := false
	cleanupOnFail := func() {
		if connectedHere && s.transfer != nil {
			_ = s.transfer.Close()
			connectedHere = false
		}
		if preparedHere && s.fetcher != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			_ = s.fetcher.Cleanup(cleanupCtx)
			cancel()
			preparedHere = false
		}
	}
	fail := func(err error) (*PackagePreview, error) {
		cleanupOnFail()
		_ = s.repo.FinalizarSessao(context.Background(), session.ID, core.SessaoFalhou, time.Now().UTC())
		return nil, err
	}
	if cp := s.repo; cp != nil {
		now := time.Now().UTC()
		if err := cp.RegistrarEtapa(ctx, &core.EtapaSessao{
			SessaoID: session.ID, Etapa: 1, Nome: core.NomeEtapa(1),
			Status: core.EtapaConcluida, IniciadaEm: &now, FinalizadaEm: &now,
		}); err != nil {
			return fail(fmt.Errorf("registrar etapa 1: %w", err))
		}
		_ = cp.AtualizarEtapaAtual(ctx, session.ID, 2)
		for _, pkg := range s.cfg.Packages {
			_ = cp.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{SessaoID: session.ID, Nome: pkg})
		}
	}

	if binder, ok := s.fetcher.(core.SessionBinder); ok {
		if err := binder.BindSession(session.ID); err != nil {
			return fail(fmt.Errorf("vincular sessão ao fetcher: %w", err))
		}
	}
	if err := s.fetcher.Prepare(ctx, s.cfg.TargetOS, s.cfg.TargetVersion, s.cfg.TargetArch); err != nil {
		return fail(fmt.Errorf("preparar ambiente para o diff: %w", err))
	}
	preparedHere = true

	if err := s.transfer.Connect(ctx, s.cfg.SSH); err != nil {
		return fail(fmt.Errorf("conectar SSH para o diff: %w", err))
	}
	connectedHere = true

	candidates, err := s.fetcher.(core.PackageVersionResolver).ResolveCandidates(ctx, s.cfg.Packages, s.cfg.IncludeOptional)
	if err != nil {
		return fail(fmt.Errorf("resolver versões candidatas: %w", err))
	}

	installed := []core.InstalledPackage{}
	if mode == core.VersionReportAll {
		lister, ok := s.inspect.(core.InstalledPackageLister)
		if !ok {
			return fail(fmt.Errorf("inspetor não suporta inventário completo"))
		}
		installed, err = lister.ListInstalled(ctx)
	} else {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		installed, err = s.inspect.InspectPackages(ctx, names)
	}
	if err != nil {
		return fail(fmt.Errorf("coletar pacotes instalados: %w", err))
	}

	data, upgrades := buildPackagePreview(mode, s.cfg.Packages, candidates, installed)
	encoded, err := json.Marshal(data)
	if err != nil {
		return fail(fmt.Errorf("serializar preview: %w", err))
	}
	if repo := s.repo; repo != nil {
		if err := repo.AtualizarSessaoPreview(ctx, session.ID, string(encoded)); err != nil {
			return fail(fmt.Errorf("persistir preview: %w", err))
		}
	}
	// Keep the isolated environment and SSH connection warm so RunPreviewed
	// does not pay the container startup and SSH handshake cost twice.
	// A safety TTL guarantees cleanup even if the caller forgets Abort/Close.
	s.markWarmPreview()
	return &PackagePreview{SessionID: session.ID, Data: data, Upgrades: upgrades}, nil
}

// AbortPreview records the user's refusal and finalizes the session as
// aborted. The report keeps the collected inventory, diff and decision.
func (s *Service) AbortPreview(ctx context.Context, preview *PackagePreview) error {
	_ = s.cleanupWarmResources()
	if preview == nil || preview.SessionID == 0 {
		return fmt.Errorf("preview inválido")
	}
	preview.Data.Decision = "aborted"
	if err := s.savePreview(ctx, preview); err != nil {
		return err
	}
	if err := s.repo.FinalizarSessao(ctx, preview.SessionID, core.SessaoAbortada, time.Now().UTC()); err != nil {
		return fmt.Errorf("finalizar sessão abortada: %w", err)
	}
	return s.repo.AdicionarEvento(ctx, &core.Evento{
		SessaoID: preview.SessionID, Timestamp: time.Now().UTC(), Tipo: core.EventoInfo,
		Mensagem: "instalação recusada na tela Package diff; sessão abortada",
	})
}

// RunPreviewed marks the preview as approved and runs the normal pipeline from
// step 2, reusing the session (and, when still warm, the isolated environment
// and SSH connection) created by PreviewPackageDiff.
func (s *Service) RunPreviewed(ctx context.Context, preview *PackagePreview, reporter core.PipelineReporter) (*core.PipelineResult, error) {
	if preview == nil || preview.SessionID == 0 {
		_ = s.cleanupWarmResources()
		return nil, fmt.Errorf("preview inválido")
	}
	preview.Data.Decision = "approved"
	if err := s.savePreview(ctx, preview); err != nil {
		_ = s.cleanupWarmResources()
		return nil, err
	}
	stored, err := s.repo.ObterSessao(ctx, preview.SessionID)
	if err != nil {
		_ = s.cleanupWarmResources()
		return nil, err
	}
	// Transfer ownership of warm resources to ExecutarPipeline (whose defer
	// guarantees cleanup/close).
	s.disarmWarmPreview()
	cfg := s.pipelineConfig(reporter)
	cfg.ResumeFrom = 2
	cfg.UpgradeRoots = preview.Upgrades
	return core.ExecutarPipeline(ctx, cfg, stored)
}

func (s *Service) markWarmPreview() {
	ttl := s.cfg.StageTimeout
	if ttl <= 0 {
		ttl = defaultWarmPreviewTTL
	}
	s.warmMu.Lock()
	defer s.warmMu.Unlock()
	if s.warmTimer != nil {
		s.warmTimer.Stop()
	}
	s.warmPreview = true
	s.warmTimer = time.AfterFunc(ttl, func() {
		_ = s.cleanupWarmResources()
	})
}

func (s *Service) disarmWarmPreview() {
	s.warmMu.Lock()
	defer s.warmMu.Unlock()
	if s.warmTimer != nil {
		s.warmTimer.Stop()
		s.warmTimer = nil
	}
	s.warmPreview = false
}

func (s *Service) cleanupWarmResources() error {
	s.warmMu.Lock()
	if s.warmTimer != nil {
		s.warmTimer.Stop()
		s.warmTimer = nil
	}
	if !s.warmPreview {
		s.warmMu.Unlock()
		return nil
	}
	s.warmPreview = false
	fetcherRef := s.fetcher
	transferRef := s.transfer
	s.warmMu.Unlock()

	var errs []error
	if transferRef != nil {
		if err := transferRef.Close(); err != nil {
			errs = append(errs, fmt.Errorf("fechar SSH do preview: %w", err))
		}
	}
	if fetcherRef != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := fetcherRef.Cleanup(cleanupCtx); err != nil {
			errs = append(errs, fmt.Errorf("cleanup do ambiente do preview: %w", err))
		}
	}
	return errors.Join(errs...)
}

func packageKey(name string) string {
	if i := strings.LastIndexByte(name, ':'); i > 0 {
		return name[:i]
	}
	return name
}

func buildPackagePreview(mode core.VersionReportMode, roots []string, candidates []core.CandidatePackage, installed []core.InstalledPackage) (core.PackagePreviewData, map[string]bool) {
	installedBy := make(map[string]string, len(installed))
	for _, pkg := range installed {
		installedBy[packageKey(pkg.Name)] = pkg.Version
	}
	rootSet := make(map[string]bool, len(roots))
	for _, pkg := range roots {
		rootSet[packageKey(pkg)] = true
	}
	diff := make([]core.PackageDiff, 0)
	upgrades := make(map[string]bool)
	for _, candidate := range candidates {
		prior, ok := installedBy[packageKey(candidate.Name)]
		if !ok || prior == "" || candidate.Version == "" {
			continue
		}
		if core.CompareVersions(candidate.Version, prior) > 0 {
			diff = append(diff, core.PackageDiff{
				Name: candidate.Name, From: prior, To: candidate.Version,
				IsDependency: candidate.IsDependency,
			})
			if rootSet[packageKey(candidate.Name)] {
				upgrades[candidate.Name] = true
				upgrades[packageKey(candidate.Name)] = true
			}
		}
	}
	return core.PackagePreviewData{Mode: mode, Inventory: installed, Diff: diff}, upgrades
}

func (s *Service) savePreview(ctx context.Context, preview *PackagePreview) error {
	if repo := s.repo; repo != nil {
		encoded, err := json.Marshal(preview.Data)
		if err != nil {
			return fmt.Errorf("serializar preview: %w", err)
		}
		return repo.AtualizarSessaoPreview(ctx, preview.SessionID, string(encoded))
	}
	return nil
}
