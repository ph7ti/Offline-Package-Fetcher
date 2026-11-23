package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"opf/internal/i18n"
	"opf/internal/transfer"
)

const (
	defaultStageTimeout = 10 * time.Minute
	finalizeTimeout     = 2 * time.Minute
)

type PipelineConfig struct {
	Repo           Repository
	Fetcher        PackageFetcher
	Pacote         string
	Packages       []string
	OutputBaseDir  string
	StageTimeout   time.Duration
	Logger         *slog.Logger
	Reporter       PipelineReporter
	EventSink      EventSink
	ForceReinstall bool
	UpgradeRoots   map[string]bool
	Inspector      PackageInspector

	// Transfer is optional. When set, steps 2, 8 and 9 run.
	Transfer  TransferManager
	SSH       SSHConfig
	RemoteDir string
	Image     string
	WSLDistro string

	// Installer is optional. When set, cfg.ConfirmInstall is mandatory.
	Installer      InstallExecutor
	ConfirmInstall InstallConfirmation

	// ResumeFrom > 1 retoma uma sessão existente a partir de uma etapa segura.
	ResumeFrom int
}

type PipelineResult struct {
	SessaoID            int64
	StartStep           int
	OutputDir           string
	CaminhoManifesto    string
	RemoteDir           string
	Artifacts           []PackageArtifact
	Installed           bool
	InstallDeclined     bool
	AllAlreadyInstalled bool
	AlreadyInstalled    []InstalledPackage
}

// ExecutarPipeline executa o pipeline de 13 etapas, com checkpoint e retomada
// segura a partir de cfg.ResumeFrom.
func ExecutarPipeline(ctx context.Context, cfg PipelineConfig, s *Sessao) (result *PipelineResult, err error) {
	if cfg.Repo == nil {
		return nil, errors.New(i18n.T("repositório não configurado"))
	}
	if cfg.Fetcher == nil {
		return nil, errors.New(i18n.T("fetcher não configurado"))
	}
	if s == nil {
		return nil, errors.New(i18n.T("sessão nula"))
	}
	// A resumed/previewed run may receive only the session ID. Load the stored
	// target and execution metadata before validation so target OS/version are
	// available to the installer path.
	if cfg.ResumeFrom > 1 && s.ID != 0 {
		stored, loadErr := cfg.Repo.ObterSessao(ctx, s.ID)
		if loadErr != nil {
			return nil, fmt.Errorf(i18n.T("carregar sessão para retomada: %w"), loadErr)
		}
		mergeResumeSession(s, stored)
	}
	roots := collectPackageRoots(cfg)
	if len(roots) == 0 {
		return nil, errors.New(i18n.T("nenhum pacote informado"))
	}
	if cfg.Installer != nil {
		if cfg.Transfer == nil {
			return nil, errors.New(i18n.T("instalação remota exige transferência SSH configurada"))
		}
		if cfg.ConfirmInstall == nil {
			return nil, errors.New(i18n.T("instalação remota exige confirmação explícita do usuário"))
		}
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}

	startStep := cfg.ResumeFrom
	if startStep == 0 {
		startStep = 1
	}
	if startStep < 1 || startStep > TotalEtapas {
		return nil, fmt.Errorf(i18n.T("etapa de retomada inválida: %d"), startStep)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.OutputBaseDir == "" {
		cfg.OutputBaseDir = "."
	}
	if cfg.Transfer != nil && s.HostDestino == "" {
		s.HostDestino = cfg.SSH.Host
	}

	if startStep == 1 {
		if s.IniciadaEm.IsZero() {
			s.IniciadaEm = time.Now().UTC()
		}
		s.Status = SessaoEmAndamento
		if err := cfg.Repo.CreateSessao(ctx, s); err != nil {
			return nil, fmt.Errorf(i18n.T("criar sessão: %w"), err)
		}
	} else {
		if s.ID == 0 {
			return nil, errors.New(i18n.T("retomada exige o ID da sessão"))
		}
		stored, getErr := cfg.Repo.ObterSessao(ctx, s.ID)
		if getErr != nil {
			return nil, fmt.Errorf(i18n.T("carregar sessão para retomada: %w"), getErr)
		}
		mergeResumeSession(s, stored)
		s.Status = SessaoEmAndamento
		if cpRepo, ok := cfg.Repo.(CheckpointRepository); ok {
			if reopenErr := cpRepo.ReabrirSessao(ctx, s.ID); reopenErr != nil {
				return nil, fmt.Errorf(i18n.T("reabrir sessão para retomada: %w"), reopenErr)
			}
		}
	}

	logger = logger.With("session_id", s.ID)
	cp := newCheckpoint(cfg.Repo, s.ID, cfg.Reporter, logger)
	if startStep == 1 {
		cp.start(ctx, 1)
		cp.finish(ctx, 1)
		registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoInfo, i18n.T("sessão iniciada"), map[string]any{
			"so_destino": s.TargetOS, "versao_destino": s.TargetVersion,
			"arch_destino": s.TargetArch, "metodo_fetch": s.FetchMethod,
			"include_optional": s.IncludeOptional, "pacotes": roots,
		})
	}
	if binder, ok := cfg.Fetcher.(SessionBinder); ok {
		if bindErr := binder.BindSession(s.ID); bindErr != nil {
			return nil, fmt.Errorf(i18n.T("vincular sessão ao fetcher: %w"), bindErr)
		}
	}
	persistirPacotesSolicitados(ctx, cfg.Repo, s.ID, roots)

	prepared := false
	transferConnected := false
	remoteDir := firstNonEmpty(cfg.RemoteDir, s.RemoteDir)
	if cfg.Transfer != nil {
		if remoteDir == "" {
			remoteDir = path.Join("/tmp", fmt.Sprintf("opf-sessao-%d", s.ID))
		}
		s.RemoteDir = remoteDir
	}

	defer func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
		defer cancel()

		aborted := errors.Is(err, context.Canceled) || (err == nil && errors.Is(ctx.Err(), context.Canceled))
		if aborted {
			cp.failCurrent(bgCtx, context.Canceled)
		} else if err != nil {
			cp.failCurrent(bgCtx, err)
		}

		if prepared {
			if cleanupErr := cfg.Fetcher.Cleanup(bgCtx); cleanupErr != nil {
				logger.Error("falha no cleanup do ambiente isolado", "erro", cleanupErr)
				err = errors.Join(err, fmt.Errorf(i18n.T("cleanup do ambiente isolado: %w"), cleanupErr))
			}
		}
		cp.finish(bgCtx, 13)

		status := SessaoConcluida
		switch {
		case aborted:
			status = SessaoAbortada
		case err != nil:
			status = SessaoFalhou
		}
		if finalizeErr := cfg.Repo.FinalizarSessao(bgCtx, s.ID, status, time.Now().UTC()); finalizeErr != nil {
			err = errors.Join(err, fmt.Errorf(i18n.T("finalizar sessão: %w"), finalizeErr))
		}
		tipo := EventoInfo
		mensagem := i18n.T("sessão concluída")
		if err != nil {
			tipo = EventoErro
			mensagem = i18n.T("sessão falhou: %s", err.Error())
		}
		if aborted {
			mensagem = i18n.T("sessão abortada pelo usuário")
		}
		registrarEvento(bgCtx, cfg.Repo, cfg.EventSink, logger, s.ID, tipo, mensagem, nil)
		if err != nil {
			result = nil
		}
	}()

	if cfg.Transfer != nil {
		tctx, cancel := stageContext(ctx, cfg.StageTimeout)
		connectErr := connectTransfer(tctx, cfg.Transfer, cfg.SSH, logger)
		cancel()
		if connectErr != nil {
			return nil, fmt.Errorf(i18n.T("conectar SSH ao destino: %w"), connectErr)
		}
		transferConnected = true
		defer func() {
			if !transferConnected {
				return
			}
			bg, bgCancel := context.WithTimeout(context.Background(), finalizeTimeout)
			defer bgCancel()
			if (errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)) && remoteDir != "" {
				if cleaner, ok := cfg.Transfer.(RemoteCleaner); ok {
					if cleanupErr := cleaner.RemoveRemoteDir(bg, remoteDir); cleanupErr != nil {
						logger.Warn("falha ao remover diretório remoto", "erro", cleanupErr, "dir", remoteDir)
					}
				}
			}
			if closeErr := cfg.Transfer.Close(); closeErr != nil {
				logger.Warn("falha ao fechar conexão SSH", "erro", closeErr)
			}
		}()
		if progressSetter, ok := cfg.Transfer.(interface{ SetProgress(transfer.ProgressFunc) }); ok && cfg.Reporter != nil {
			progressSetter.SetProgress(func(stage string, current, total int64) {
				cfg.Reporter.Progress(stage, current, total)
			})
		}
	}
	if collector, ok := cfg.Inspector.(HostInfoCollector); ok {
		info, collectErr := collector.CollectHostInfo(ctx)
		if collectErr != nil {
			logger.Warn("não foi possível coletar informações do host", "erro", collectErr)
		} else {
			info.Host = s.HostDestino
			if info.Usuario == "" {
				info.Usuario = cfg.SSH.User
			}
			if data, marshalErr := json.Marshal(info); marshalErr == nil {
				s.HostInfoJSON = string(data)
				if cp, ok := cfg.Repo.(CheckpointRepository); ok {
					if saveErr := cp.AtualizarHostInfo(ctx, s.ID, s.HostInfoJSON); saveErr != nil {
						logger.Warn("não foi possível persistir host info", "erro", saveErr)
					}
				}
			}
		}
	}
	if repoSetter, ok := cfg.Fetcher.(ReporterSetter); ok {
		repoSetter.SetReporter(cfg.Reporter)
	}

	// Passo 2: pré-check remoto.
	var alreadyInstalled []InstalledPackage
	if startStep <= 2 && cfg.Transfer != nil && cfg.Inspector != nil {
		cp.start(ctx, 2)
		installed, inspectErr := cfg.Inspector.InspectPackages(ctx, roots)
		if inspectErr != nil {
			return nil, fmt.Errorf(i18n.T("pré-check remoto: %w"), inspectErr)
		}
		alreadyInstalled = installed
		if !cfg.ForceReinstall {
			for _, p := range installed {
				if cfg.UpgradeRoots[p.Name] {
					continue
				}
				if addErr := cfg.Repo.AdicionarPacote(ctx, &Pacote{
					SessaoID: s.ID, Nome: p.Name, Versao: p.Version,
					Arquitetura: p.Arch, Status: PacoteJaInstalado,
				}); addErr != nil {
					return nil, fmt.Errorf(i18n.T("registrar pacote já instalado %q: %w"), p.Name, addErr)
				}
			}
		}
		if !cfg.ForceReinstall && len(installed) > 0 {
			installedSet := make(map[string]struct{}, len(installed))
			for _, p := range installed {
				installedSet[p.Name] = struct{}{}
			}
			filtered := roots[:0]
			for _, root := range roots {
				if _, ok := installedSet[root]; !ok || cfg.UpgradeRoots[root] {
					filtered = append(filtered, root)
				}
			}
			roots = filtered
		}
		registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoPreCheck, i18n.T("pré-check remoto concluído"), map[string]any{
			"instalados": len(installed), "roots_restantes": len(roots),
		})
		cp.finish(ctx, 2)
	} else if startStep <= 2 {
		cp.skip(ctx, 2)
	}

	if len(roots) == 0 {
		cp.skip(ctx, 3)
		cp.skip(ctx, 4)
		cp.skip(ctx, 5)
		cp.skip(ctx, 6)
		cp.skip(ctx, 7)
		cp.skip(ctx, 8)
		cp.skip(ctx, 9)
		cp.skip(ctx, 10)
		cp.skip(ctx, 11)
		cp.start(ctx, 12)
		cp.finish(ctx, 12)
		return &PipelineResult{
			SessaoID: s.ID, StartStep: startStep, RemoteDir: remoteDir,
			AllAlreadyInstalled: true, AlreadyInstalled: alreadyInstalled,
		}, nil
	}

	outDir := firstNonEmpty(s.OutputDir, filepath.Join(cfg.OutputBaseDir, fmt.Sprintf("sessao-%d", s.ID)))
	s.OutputDir = outDir
	manifestPath := filepath.Join(outDir, transfer.NomeManifesto)
	var artifacts []PackageArtifact
	var manifest transfer.Manifesto

	if startStep <= 7 {
		cp.start(ctx, 3)
		pctx, cancel := stageContext(ctx, cfg.StageTimeout)
		prepareErr := cfg.Fetcher.Prepare(pctx, s.TargetOS, s.TargetVersion, s.TargetArch)
		cancel()
		if prepareErr != nil {
			return nil, fmt.Errorf(i18n.T("preparar ambiente isolado: %w"), prepareErr)
		}
		prepared = true
		persistExecution(ctx, cfg, s, outDir, remoteDir, logger)
		logger.Info("ambiente isolado criado")
		registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoInfo, i18n.T("ambiente isolado criado"), map[string]any{
			"metodo_fetch": s.FetchMethod, "arch": s.TargetArch,
		})
		cp.finish(ctx, 3)

		cp.start(ctx, 4)
		dctx, cancelDownload := stageContext(ctx, cfg.StageTimeout)
		downloaded, downloadErr := downloadRoots(dctx, cfg.Fetcher, roots, s.IncludeOptional)
		cancelDownload()
		if downloadErr != nil {
			return nil, fmt.Errorf(i18n.T("baixar pacotes %v: %w"), roots, downloadErr)
		}
		if len(downloaded) == 0 {
			return nil, fmt.Errorf(i18n.T("nenhum artefato retornado para %v"), roots)
		}
		artifacts = downloaded
		cp.finish(ctx, 4)
		cp.start(ctx, 5)
		cp.finish(ctx, 5)
		cp.start(ctx, 6)
		if clearErr := os.RemoveAll(outDir); clearErr != nil {
			return nil, fmt.Errorf(i18n.T("limpar saída da sessão para novo download: %w"), clearErr)
		}
		if persistErr := persistArtifacts(outDir, artifacts); persistErr != nil {
			return nil, fmt.Errorf(i18n.T("persistir artefatos da sessão: %w"), persistErr)
		}
		cp.finish(ctx, 6)

		cp.start(ctx, 7)
		manifest, err = gerarManifesto(outDir, artifacts)
		if err != nil {
			return nil, err
		}
		manifestPath = manifest.Caminho
		if regErr := registrarPacotes(ctx, cfg.Repo, s.ID, artifacts); regErr != nil {
			return nil, regErr
		}
		cp.finish(ctx, 7)
		registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoDownload, i18n.T("manifesto SHA256SUMS gerado"), map[string]any{
			"manifesto": manifestPath, "output_dir": outDir, "artefatos": len(artifacts),
		})
	} else {
		artifacts, manifest, err = carregarArtefatosRetomada(ctx, cfg.Repo, s.ID, outDir)
		if err != nil {
			return nil, err
		}
		manifestPath = manifest.Caminho
		persistExecution(ctx, cfg, s, outDir, remoteDir, logger)
	}

	if len(artifacts) > 0 && startStep <= 10 && cfg.Transfer != nil && cfg.Inspector != nil {
		if versionErr := capturarVersoesAnteriores(ctx, cfg.Repo, s.ID, artifacts, cfg.Inspector); versionErr != nil {
			logger.Warn("não foi possível capturar versões anteriores", "erro", versionErr)
		}
	}

	installed := false
	declined := false
	if cfg.Transfer != nil {
		cp.start(ctx, 8)
		if startStep <= 8 {
			files := make([]string, 0, len(artifacts)+1)
			for _, artifact := range artifacts {
				files = append(files, artifact.LocalPath)
			}
			files = append(files, manifestPath)
			tctx, cancel := stageContext(ctx, cfg.StageTimeout)
			uploadErr := cfg.Transfer.Upload(tctx, files, remoteDir)
			cancel()
			if uploadErr != nil {
				return nil, fmt.Errorf(i18n.T("enviar artefatos via SFTP: %w"), uploadErr)
			}
			registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoTransferencia, i18n.T("artefatos enviados via SFTP"), map[string]any{
				"remote_dir": remoteDir, "arquivos": len(files),
			})
		}
		cp.finish(ctx, 8)

		cp.start(ctx, 9)
		if startStep <= 9 {
			tctx, cancel := stageContext(ctx, cfg.StageTimeout)
			verifyErr := cfg.Transfer.VerifyChecksums(tctx, path.Join(remoteDir, transfer.NomeManifesto), remoteDir)
			cancel()
			if verifyErr != nil {
				return nil, fmt.Errorf(i18n.T("verificar checksums no destino: %w"), verifyErr)
			}
		}
		p3OK := true
		marcarVerificacoes(ctx, cfg.Repo, s.ID, &p3OK, nil)
		if statusErr := cfg.Repo.AtualizarPacotesStatus(ctx, s.ID, PacoteBaixado, PacoteEnviado); statusErr != nil {
			return nil, fmt.Errorf(i18n.T("registrar pacotes como enviados: %w"), statusErr)
		}
		cp.finish(ctx, 9)
	} else {
		cp.skip(ctx, 8)
		cp.skip(ctx, 9)
	}

	if cfg.Installer != nil {
		if startStep <= 10 {
			cp.start(ctx, 10)
			if binder, ok := cfg.Installer.(RemoteDirBinder); ok {
				if bindErr := binder.SetRemoteDir(remoteDir); bindErr != nil {
					return nil, fmt.Errorf(i18n.T("configurar diretório remoto da instalação: %w"), bindErr)
				}
			}
			approved, confirmErr := cfg.ConfirmInstall(ctx, artifacts)
			if confirmErr != nil {
				return nil, fmt.Errorf(i18n.T("confirmar instalação: %w"), confirmErr)
			}
			if !approved {
				if statusErr := cfg.Repo.AtualizarPacotesStatus(ctx, s.ID, PacoteEnviado, PacoteEnviadoNaoInstalado); statusErr != nil {
					return nil, fmt.Errorf(i18n.T("registrar pacotes como enviados não instalados: %w"), statusErr)
				}
				declined = true
				cp.finish(ctx, 10)
				cp.skip(ctx, 11)
				registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoInfo, i18n.T("instalação recusada pelo usuário"), map[string]any{
					"remote_dir": remoteDir, "pacotes": len(artifacts),
				})
			} else {
				cp.finish(ctx, 10)
				cp.start(ctx, 11)
				command, cmdErr := cfg.Installer.InstallCommand(artifacts)
				if cmdErr != nil {
					return nil, fmt.Errorf(i18n.T("montar comando de instalação: %w"), cmdErr)
				}
				ictx, cancel := stageContext(ctx, cfg.StageTimeout)
				installErr := cfg.Installer.Execute(ictx, cfg.Transfer, command)
				cancel()
				if installErr != nil {
					bg, bgCancel := context.WithTimeout(context.Background(), finalizeTimeout)
					_ = cfg.Repo.AtualizarPacotesStatus(bg, s.ID, PacoteEnviado, PacoteFalhou)
					registrarEvento(bg, cfg.Repo, cfg.EventSink, logger, s.ID, EventoErro, i18n.T("falha na instalação remota"), map[string]any{
						"remote_dir": remoteDir, "pacotes": len(artifacts),
					})
					bgCancel()
					return nil, fmt.Errorf(i18n.T("instalar pacotes no destino: %w"), installErr)
				}
				if statusErr := cfg.Repo.AtualizarPacotesStatus(ctx, s.ID, PacoteEnviado, PacoteInstalado); statusErr != nil {
					return nil, fmt.Errorf(i18n.T("registrar pacotes como instalados: %w"), statusErr)
				}
				instOK := true
				marcarVerificacoes(ctx, cfg.Repo, s.ID, nil, &instOK)
				installed = true
				cp.finish(ctx, 11)
				registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoInstalacao, i18n.T("pacotes instalados no destino"), map[string]any{
					"remote_dir": remoteDir, "pacotes": len(artifacts),
				})
			}
		} else {
			cp.skip(ctx, 10)
			cp.skip(ctx, 11)
			installed = true
		}
	} else {
		cp.skip(ctx, 10)
		cp.skip(ctx, 11)
	}

	cp.start(ctx, 12)
	cp.finish(ctx, 12)
	registrarEvento(ctx, cfg.Repo, cfg.EventSink, logger, s.ID, EventoInfo, i18n.T("resultado registrado"), map[string]any{
		"artefatos": len(artifacts), "instalado": installed, "recusado": declined,
	})
	logger.Info("pipeline concluído", "artefatos", len(artifacts), "instalado", installed)
	return &PipelineResult{
		SessaoID: s.ID, StartStep: startStep, OutputDir: outDir,
		CaminhoManifesto: manifestPath, RemoteDir: remoteDir,
		Artifacts: artifacts, Installed: installed, InstallDeclined: declined,
		AlreadyInstalled: alreadyInstalled,
	}, nil
}

// PlanejarRetomada devolve a etapa segura a partir da qual a sessão pode
// continuar. Instalação nunca é retomada automaticamente em etapa >10.
func PlanejarRetomada(ctx context.Context, repo Repository, sessaoID int64) (int, error) {
	cp, ok := repo.(CheckpointRepository)
	if !ok {
		return 1, nil
	}
	sessao, err := repo.ObterSessao(ctx, sessaoID)
	if err != nil {
		return 0, err
	}
	etapas, err := cp.ListarEtapas(ctx, sessaoID)
	if err != nil {
		return 0, err
	}
	if len(etapas) == 0 {
		return 1, nil
	}
	status := make(map[int]EtapaStatus, len(etapas))
	for _, etapa := range etapas {
		status[etapa.Etapa] = etapa.Status
	}
	outputDir := sessao.OutputDir
	if outputDir == "" {
		return 3, nil
	}
	if status[7] == EtapaConcluida || status[8] == EtapaConcluida || status[9] == EtapaConcluida {
		if _, err := transfer.VerificarManifesto(filepath.Join(outputDir, transfer.NomeManifesto), outputDir); err != nil {
			return 3, nil
		}
	}
	switch {
	case status[12] == EtapaConcluida:
		return 12, nil
	case status[11] == EtapaFalhou:
		return 10, nil
	case status[11] == EtapaConcluida:
		return 12, nil
	case status[10] == EtapaConcluida && status[11] == EtapaPulada:
		return 12, nil
	case status[10] == EtapaConcluida:
		return 10, nil
	case status[9] == EtapaConcluida:
		return 10, nil
	case status[8] == EtapaConcluida:
		return 9, nil
	case status[7] == EtapaConcluida:
		return 8, nil
	case status[3] == EtapaConcluida || status[4] == EtapaConcluida || status[5] == EtapaConcluida || status[6] == EtapaConcluida:
		return 3, nil
	default:
		return 3, nil
	}
}

func connectTransfer(ctx context.Context, tm TransferManager, cfg SSHConfig, logger *slog.Logger) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := tm.Connect(ctx, cfg); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == 3 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
		logger.Warn("tentativa de conexão SSH falhou; repetindo", "tentativa", attempt, "erro", lastErr)
	}
	return lastErr
}

func collectPackageRoots(cfg PipelineConfig) []string {
	values := append([]string(nil), cfg.Packages...)
	if cfg.Pacote != "" {
		values = append(values, cfg.Pacote)
	}
	seen := make(map[string]struct{}, len(values))
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		roots = append(roots, value)
	}
	return roots
}

func downloadRoots(ctx context.Context, fetcher PackageFetcher, roots []string, includeOptional bool) ([]PackageArtifact, error) {
	if multi, ok := fetcher.(MultiPackageFetcher); ok {
		return multi.DownloadPackages(ctx, roots, includeOptional)
	}
	seen := make(map[string]PackageArtifact)
	for _, root := range roots {
		artifacts, err := fetcher.Download(ctx, root, includeOptional)
		if err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			if _, ok := seen[artifact.Name]; !ok {
				seen[artifact.Name] = artifact
			}
		}
	}
	rootSet := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		name := root
		if idx := strings.IndexByte(name, ':'); idx >= 0 {
			name = name[:idx]
		}
		rootSet[name] = struct{}{}
	}
	out := make([]PackageArtifact, 0, len(seen))
	for _, artifact := range seen {
		_, isRoot := rootSet[artifact.Name]
		artifact.IsDependency = !isRoot
		out = append(out, artifact)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func gerarManifesto(outDir string, artifacts []PackageArtifact) (transfer.Manifesto, error) {
	paths := make([]string, len(artifacts))
	for i := range artifacts {
		paths[i] = artifacts[i].LocalPath
	}
	manifest, err := transfer.GerarManifesto(outDir, paths)
	if err != nil {
		return transfer.Manifesto{}, fmt.Errorf(i18n.T("gerar manifesto SHA256SUMS: %w"), err)
	}
	verified, err := transfer.VerificarManifesto(manifest.Caminho, outDir)
	if err != nil {
		return transfer.Manifesto{}, fmt.Errorf(i18n.T("verificar manifesto SHA256SUMS: %w"), err)
	}
	if len(verified.Arquivos) != len(artifacts) {
		return transfer.Manifesto{}, fmt.Errorf(i18n.T("manifesto incompleto: %d entradas para %d artefatos"), len(verified.Arquivos), len(artifacts))
	}
	hashes := make(map[string]string, len(verified.Arquivos))
	for _, entry := range verified.Arquivos {
		hashes[entry.Nome] = entry.SHA256
	}
	for i := range artifacts {
		hash, ok := hashes[filepath.Base(artifacts[i].LocalPath)]
		if !ok {
			return transfer.Manifesto{}, fmt.Errorf(i18n.T("artefato %q ausente do manifesto"), artifacts[i].LocalPath)
		}
		artifacts[i].SHA256 = hash
		artifacts[i].SHA256P2 = hash
	}
	return manifest, nil
}

func registrarPacotes(ctx context.Context, repo Repository, sessaoID int64, artifacts []PackageArtifact) error {
	for i := range artifacts {
		artifact := &artifacts[i]
		if err := repo.AdicionarPacote(ctx, &Pacote{
			SessaoID: sessaoID, Nome: artifact.Name, Versao: artifact.Version,
			Arquitetura: artifact.Arch, Status: PacoteBaixado,
			EhDependencia: artifact.IsDependency, CaminhoLocal: artifact.LocalPath,
			SHA256: artifact.SHA256, SHA256P1: artifact.SHA256P1, SHA256P2: artifact.SHA256P2,
			P3Verificado: artifact.P3Verified, InstaladoVerificado: artifact.Installed,
		}); err != nil {
			return fmt.Errorf(i18n.T("registrar pacote %q: %w"), artifact.Name, err)
		}
	}
	return nil
}

func carregarArtefatosRetomada(ctx context.Context, repo Repository, sessaoID int64, outDir string) ([]PackageArtifact, transfer.Manifesto, error) {
	pacotes, err := repo.ListarPacotes(ctx, sessaoID)
	if err != nil {
		return nil, transfer.Manifesto{}, fmt.Errorf(i18n.T("carregar pacotes da retomada: %w"), err)
	}
	artifacts := make([]PackageArtifact, 0, len(pacotes))
	for _, p := range pacotes {
		if p.Status != PacoteBaixado && p.Status != PacoteEnviado && p.Status != PacoteEnviadoNaoInstalado {
			continue
		}
		if p.CaminhoLocal == "" {
			continue
		}
		artifacts = append(artifacts, PackageArtifact{
			Name: p.Nome, Version: p.Versao, Arch: p.Arquitetura,
			LocalPath: p.CaminhoLocal, IsDependency: p.EhDependencia,
			SHA256: p.SHA256, SHA256P1: p.SHA256P1, SHA256P2: p.SHA256P2,
			P3Verified: p.P3Verificado, Installed: p.InstaladoVerificado,
		})
	}
	if len(artifacts) == 0 {
		return nil, transfer.Manifesto{}, errors.New(i18n.T("nenhum artefato persistido para retomada"))
	}
	manifestPath := filepath.Join(outDir, transfer.NomeManifesto)
	verified, err := transfer.VerificarManifesto(manifestPath, outDir)
	if err != nil {
		return nil, transfer.Manifesto{}, fmt.Errorf(i18n.T("manifesto da retomada inválido: %w"), err)
	}
	return artifacts, transfer.Manifesto{Caminho: manifestPath, Arquivos: verified.Arquivos}, nil
}

func capturarVersoesAnteriores(ctx context.Context, repo Repository, sessaoID int64, artifacts []PackageArtifact, inspector PackageInspector) error {
	cp, ok := repo.(CheckpointRepository)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		names = append(names, artifact.Name)
	}
	installed, err := inspector.InspectPackages(ctx, names)
	if err != nil {
		return err
	}
	prior := make(map[string]string, len(installed))
	for _, pkg := range installed {
		prior[pkg.Name] = pkg.Version
	}
	pacotes, err := repo.ListarPacotes(ctx, sessaoID)
	if err != nil {
		return err
	}
	for i := range pacotes {
		version, ok := prior[pacotes[i].Nome]
		if !ok || version == "" {
			continue
		}
		if err := cp.AtualizarPacoteVersaoAnterior(ctx, pacotes[i].ID, version); err != nil {
			return err
		}
	}
	return nil
}
func marcarVerificacoes(ctx context.Context, repo Repository, sessaoID int64, p3, instalado *bool) {
	cp, ok := repo.(CheckpointRepository)
	if !ok {
		return
	}
	pacotes, err := repo.ListarPacotes(ctx, sessaoID)
	if err != nil {
		return
	}
	for i := range pacotes {
		_ = cp.AtualizarPacoteVerificacoes(ctx, pacotes[i].ID, "", "", p3, instalado)
	}
}

func persistirPacotesSolicitados(ctx context.Context, repo Repository, sessaoID int64, roots []string) {
	cp, ok := repo.(CheckpointRepository)
	if !ok {
		return
	}
	existing, err := cp.ListarPacotesSolicitados(ctx, sessaoID)
	if err == nil && len(existing) > 0 {
		return
	}
	for _, root := range roots {
		_ = cp.AdicionarPacoteSolicitado(ctx, &PacoteSolicitado{SessaoID: sessaoID, Nome: root})
	}
}

func persistExecution(ctx context.Context, cfg PipelineConfig, s *Sessao, outDir, remoteDir string, logger *slog.Logger) {
	cp, ok := cfg.Repo.(CheckpointRepository)
	if !ok {
		return
	}
	envID := ""
	if identifier, ok := cfg.Fetcher.(interface{ EnvironmentID() string }); ok {
		envID = identifier.EnvironmentID()
	}
	if err := cp.AtualizarSessaoExecucao(ctx, s.ID, outDir, remoteDir, cfg.Image, cfg.WSLDistro, envID, s.ConfigJSON); err != nil {
		logger.Warn("não foi possível persistir estado da sessão", "erro", err)
	}
}

func mergeResumeSession(dst, stored *Sessao) {
	if dst.TargetOS == "" {
		dst.TargetOS = stored.TargetOS
	}
	if dst.TargetVersion == "" {
		dst.TargetVersion = stored.TargetVersion
	}
	if dst.TargetArch == "" {
		dst.TargetArch = stored.TargetArch
	}
	if dst.FetchMethod == "" {
		dst.FetchMethod = stored.FetchMethod
	}
	if dst.HostDestino == "" {
		dst.HostDestino = stored.HostDestino
	}
	if dst.OutputDir == "" {
		dst.OutputDir = stored.OutputDir
	}
	if dst.RemoteDir == "" {
		dst.RemoteDir = stored.RemoteDir
	}
	if dst.IniciadaEm.IsZero() {
		dst.IniciadaEm = stored.IniciadaEm
	}
	if dst.ConfigJSON == "" {
		dst.ConfigJSON = stored.ConfigJSON
	}
	if dst.ImagemDocker == "" {
		dst.ImagemDocker = stored.ImagemDocker
	}
	if dst.WSLDistro == "" {
		dst.WSLDistro = stored.WSLDistro
	}
	if dst.AmbienteID == "" {
		dst.AmbienteID = stored.AmbienteID
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type checkpoint struct {
	repo     CheckpointRepository
	enabled  bool
	sessaoID int64
	reporter PipelineReporter
	logger   *slog.Logger
	current  int
}

func newCheckpoint(repo Repository, sessaoID int64, reporter PipelineReporter, logger *slog.Logger) *checkpoint {
	cp, ok := repo.(CheckpointRepository)
	return &checkpoint{repo: cp, enabled: ok, sessaoID: sessaoID, reporter: reporter, logger: logger}
}

func (c *checkpoint) start(ctx context.Context, step int) {
	c.current = step
	if c.reporter != nil {
		c.reporter.StepStarted(step, NomeEtapa(step))
	}
	if !c.enabled {
		return
	}
	now := time.Now().UTC()
	_ = c.repo.RegistrarEtapa(ctx, &EtapaSessao{SessaoID: c.sessaoID, Etapa: step, Nome: NomeEtapa(step), Status: EtapaEmAndamento, IniciadaEm: &now})
	_ = c.repo.AtualizarEtapaAtual(ctx, c.sessaoID, step)
}

func (c *checkpoint) finish(ctx context.Context, step int) {
	if c.reporter != nil {
		c.reporter.StepFinished(step, NomeEtapa(step))
	}
	if !c.enabled {
		return
	}
	now := time.Now().UTC()
	_ = c.repo.RegistrarEtapa(ctx, &EtapaSessao{SessaoID: c.sessaoID, Etapa: step, Nome: NomeEtapa(step), Status: EtapaConcluida, FinalizadaEm: &now})
}

func (c *checkpoint) skip(ctx context.Context, step int) {
	if c.reporter != nil {
		c.reporter.StepFinished(step, NomeEtapa(step))
	}
	if !c.enabled {
		return
	}
	now := time.Now().UTC()
	_ = c.repo.RegistrarEtapa(ctx, &EtapaSessao{SessaoID: c.sessaoID, Etapa: step, Nome: NomeEtapa(step), Status: EtapaPulada, FinalizadaEm: &now})
}

func (c *checkpoint) failCurrent(ctx context.Context, cause error) {
	if !c.enabled || c.current == 0 {
		return
	}
	now := time.Now().UTC()
	_ = c.repo.RegistrarEtapa(ctx, &EtapaSessao{
		SessaoID: c.sessaoID, Etapa: c.current, Nome: NomeEtapa(c.current),
		Status: EtapaFalhou, FinalizadaEm: &now, Erro: cause.Error(),
	})
	if c.reporter != nil {
		c.reporter.StepFailed(c.current, NomeEtapa(c.current), cause)
	}
}

func stageContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultStageTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func registrarEvento(ctx context.Context, repo Repository, sink EventSink, logger *slog.Logger, sessaoID int64, tipo, mensagem string, detalhes map[string]any) {
	evento := &Evento{SessaoID: sessaoID, Timestamp: time.Now().UTC(), Tipo: tipo, Mensagem: mensagem}
	if len(detalhes) > 0 {
		encoded, err := json.Marshal(detalhes)
		if err != nil {
			logger.Warn("não foi possível serializar detalhes do evento", "erro", err)
		} else {
			evento.Detalhes = string(encoded)
		}
	}
	if err := repo.AdicionarEvento(ctx, evento); err != nil {
		logger.Warn("não foi possível registrar evento", "erro", err, "tipo", tipo)
		return
	}
	if sink != nil {
		if err := sink.Emit(ctx, *evento); err != nil {
			logger.Warn("não foi possível enfileirar evento no Syslog", "erro", err, "tipo", tipo)
		}
	}
}

func persistArtifacts(dir string, artifacts []PackageArtifact) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf(i18n.T("criar diretório de saída: %w"), err)
	}
	seen := make(map[string]struct{}, len(artifacts))
	for i := range artifacts {
		src := artifacts[i].LocalPath
		if src == "" {
			return fmt.Errorf(i18n.T("artefato %q sem caminho local"), artifacts[i].Name)
		}
		name := filepath.Base(src)
		if name == "." || name == string(filepath.Separator) || name == "" {
			return fmt.Errorf(i18n.T("nome de artefato inválido: %q"), src)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf(i18n.T("artefato duplicado: %q"), name)
		}
		seen[name] = struct{}{}
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf(i18n.T("artefato já existe no destino: %q"), dst)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(i18n.T("inspecionar destino %q: %w"), dst, err)
		}
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf(i18n.T("mover artefato %q: %w"), name, err)
		}
		artifacts[i].LocalPath = dst
	}
	return nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if _, statErr := os.Stat(src); statErr != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		out.Close()
		if !keep {
			os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := in.Close(); err != nil {
		return err
	}
	keep = true
	return os.Remove(src)
}
