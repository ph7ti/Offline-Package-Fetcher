package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/inspector"
	"opf/internal/installer"
	"opf/internal/observability"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

type Config struct {
	StorePath     string
	OutputBaseDir string
	TempDir       string
	TargetOS      string
	TargetVersion string
	TargetArch    string
	Packages      []string

	IncludeOptional   bool
	FetchMethod       string // docker, podman, wsl ou auto
	ContainerHost     string
	Image             string
	WSLDistro         string
	Proxy             core.ProxyConfig
	OfflineBundlePath string
	SSH               core.SSHConfig
	RemoteDir         string
	Repositories      []core.PackageRepository
	ForceReinstall    bool
	VersionReport     core.VersionReportMode
	StageTimeout      time.Duration
	Logger            *slog.Logger
	Reporter          core.PipelineReporter

	Install        bool
	UseSudo        bool
	SudoPassword   string
	ConfirmInstall core.InstallConfirmation

	SyslogEnabled bool
	SyslogHost    string
	SyslogPort    int
	SyslogProto   string
	SyslogTag     string
}

type Service struct {
	cfg        Config
	repo       *store.Store
	fetcher    core.PackageFetcher
	transfer   core.TransferManager
	install    core.InstallExecutor
	inspect    core.PackageInspector
	eventSink  core.EventSink
	sinkCloser core.EventSinkCloser

	warmMu      sync.Mutex
	warmPreview bool
	warmTimer   *time.Timer
}

func New(cfg Config) (*Service, error) {
	if cfg.TargetOS == "" || cfg.TargetVersion == "" || cfg.TargetArch == "" {
		return nil, errors.New("SO, versão e arquitetura de destino são obrigatórios")
	}
	cfg.TargetOS = strings.ToLower(strings.TrimSpace(cfg.TargetOS))
	cfg.Packages = uniqueStrings(cfg.Packages)
	if cfg.StorePath == "" {
		return nil, errors.New("caminho do SQLite não informado")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.FetchMethod == "" {
		cfg.FetchMethod = "docker"
	}
	if cfg.TempDir != "" {
		if err := os.MkdirAll(cfg.TempDir, 0o700); err != nil {
			return nil, fmt.Errorf("criar diretório temporário: %w", err)
		}
	}

	family, err := familyForOS(cfg.TargetOS)
	if err != nil {
		return nil, err
	}
	cfg.Repositories, err = core.NormalizePackageRepositories(cfg.Repositories, family)
	if err != nil {
		return nil, fmt.Errorf("repositórios adicionais: %w", err)
	}
	if err := cfg.Proxy.Validate(false); err != nil {
		return nil, fmt.Errorf("configuração de proxy: %w", err)
	}
	cfg.FetchMethod = resolveFetchMethod(cfg.FetchMethod, family)
	if cfg.VersionReport == "" {
		if cfg.SSH.Host == "" {
			cfg.VersionReport = core.VersionReportNone
		} else {
			cfg.VersionReport = core.VersionReportDefault
		}
	}
	if cfg.VersionReport != core.VersionReportNone && cfg.SSH.Host == "" {
		return nil, errors.New("coleta de versões exige SSH configurado")
	}
	if cfg.FetchMethod == "wsl" && family != "apt" {
		return nil, errors.New("WSL suporta apenas famílias APT")
	}
	if cfg.Install {
		if cfg.SSH.Host == "" {
			return nil, errors.New("instalação exige SSH configurado")
		}
		if cfg.ConfirmInstall == nil {
			return nil, errors.New("instalação exige confirmação explícita")
		}
		if cfg.UseSudo && cfg.SudoPassword == "" {
			return nil, errors.New("sudo exige a senha da conta SSH")
		}
	}

	repo, err := store.Open(cfg.StorePath)
	if err != nil {
		return nil, err
	}
	service := &Service{cfg: cfg, repo: repo}

	resolverFactory := func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
		switch family {
		case "rpm":
			return resolver.NewYumDnfResolver(exec, arch, workDir)
		case "apk":
			return resolver.NewApkResolver(exec, arch, workDir)
		default:
			return resolver.NewAptResolver(exec, arch, workDir)
		}
	}

	switch cfg.FetchMethod {
	case "docker", "podman":
		options := []fetcher.Option{
			fetcher.WithLogger(cfg.Logger),
			fetcher.WithResolverFactory(resolverFactory),
			fetcher.WithRuntimeKind(cfg.FetchMethod),
		}
		if cfg.ContainerHost != "" {
			options = append(options, fetcher.WithContainerHost(cfg.ContainerHost))
		}
		if !cfg.Proxy.Empty() {
			options = append(options, fetcher.WithProxy(cfg.Proxy))
		}
		if cfg.Image != "" {
			options = append(options, fetcher.WithImage(cfg.Image))
		}
		if cfg.TempDir != "" {
			options = append(options, fetcher.WithTempDir(cfg.TempDir))
		}
		if len(cfg.Repositories) > 0 {
			options = append(options, fetcher.WithRepositories(cfg.Repositories))
		}
		service.fetcher = fetcher.NewDockerFetcher(options...)
	case "wsl":
		options := []fetcher.WSLFetcherOption{
			fetcher.WithWSLDistro(cfg.WSLDistro),
			fetcher.WithWSLResolverFactory(resolverFactory),
			fetcher.WithWSLTempDir(cfg.TempDir),
			fetcher.WithWSLLogger(cfg.Logger),
		}
		if len(cfg.Repositories) > 0 {
			options = append(options, fetcher.WithWSLRepositories(cfg.Repositories))
		}
		service.fetcher = fetcher.NewWSLFetcher(options...)
	default:
		repo.Close()
		return nil, fmt.Errorf("método de fetch inválido: %q", cfg.FetchMethod)
	}

	if cfg.SSH.Host != "" {
		service.transfer = transfer.NewSSHTransferManager()
		service.inspect, err = inspector.New(family, service.transfer)
		if err != nil {
			repo.Close()
			return nil, err
		}
	}

	if cfg.Install {
		var options []installer.Option
		if cfg.UseSudo {
			options = append(options, installer.WithSudoPassword(cfg.SudoPassword))
		}
		if cfg.Reporter != nil {
			options = append(options, installer.WithReporter(cfg.Reporter))
		}
		switch family {
		case "rpm":
			service.install = installer.NewYumDnfInstaller(options...)
		case "apk":
			service.install = installer.NewApkInstaller(options...)
		default:
			service.install = installer.NewAptInstaller(options...)
		}
	}

	if cfg.SyslogEnabled {
		sink, sinkErr := observability.NewSyslogSink(observability.SyslogConfig{
			Enabled: true, Host: cfg.SyslogHost, Port: cfg.SyslogPort,
			Proto: cfg.SyslogProto, Tag: cfg.SyslogTag, Logger: cfg.Logger,
		})
		if sinkErr != nil {
			repo.Close()
			return nil, fmt.Errorf("configurar syslog: %w", sinkErr)
		}
		service.eventSink = sink
		service.sinkCloser = sink
	}

	return service, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if err := s.cleanupWarmResources(); err != nil {
		errs = append(errs, err)
	}
	if s.sinkCloser != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := s.sinkCloser.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("fechar syslog: %w", err))
		}
		cancel()
	}
	if s.repo != nil {
		if err := s.repo.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) Run(ctx context.Context) (*core.PipelineResult, error) {
	if len(s.cfg.Packages) == 0 {
		return nil, errors.New("nenhum pacote informado")
	}
	session := s.newSession()
	return core.ExecutarPipeline(ctx, s.pipelineConfig(nil), session)
}

func (s *Service) Resume(ctx context.Context, sessionID int64) (*core.PipelineResult, error) {
	start, err := core.PlanejarRetomada(ctx, s.repo, sessionID)
	if err != nil {
		return nil, err
	}
	stored, err := s.repo.ObterSessao(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if start >= 8 && s.transfer == nil {
		return nil, errors.New("retomada exige transferência SSH configurada")
	}
	if start >= 10 && start <= 11 && s.install == nil {
		return nil, errors.New("retomada no passo de instalação exige instalador e nova confirmação")
	}
	packages := s.cfg.Packages
	if len(packages) == 0 {
		solicitados, listErr := s.repo.ListarPacotesSolicitados(ctx, sessionID)
		if listErr == nil {
			for _, p := range solicitados {
				packages = append(packages, p.Nome)
			}
		}
	}
	stored.ID = sessionID
	if len(packages) > 0 {
		s.cfg.Packages = packages
	}
	cfg := s.pipelineConfig(nil)
	cfg.ResumeFrom = start
	return core.ExecutarPipeline(ctx, cfg, stored)
}

func (s *Service) Settings() Config {
	return s.cfg
}

func (s *Service) ListSessions(ctx context.Context, somenteNaoArquivadas bool) ([]core.SessaoResumo, error) {
	return s.repo.ListarSessoes(ctx, somenteNaoArquivadas)
}

func (s *Service) ArchiveSession(ctx context.Context, id int64, arquivada bool) error {
	return s.repo.ArquivarSessao(ctx, id, arquivada)
}

func (s *Service) ListPackages(ctx context.Context, sessaoID int64) ([]core.Pacote, error) {
	return s.repo.ListarPacotes(ctx, sessaoID)
}

func (s *Service) ListEvents(ctx context.Context, sessaoID int64) ([]core.Evento, error) {
	return s.repo.ListarEventos(ctx, sessaoID)
}

func (s *Service) GenerateReport(ctx context.Context, sessaoID int64) (*observability.Relatorio, error) {
	return observability.GerarRelatorio(ctx, s.repo, sessaoID)
}

// ResumeMetadata holds non-secret fields recovered from an existing session in
// SQLite so callers like opf-cli can resume by ID without re-specifying target
// and package flags.
type ResumeMetadata struct {
	TargetOS      string
	TargetVersion string
	TargetArch    string
	FetchMethod   string
	Image         string
	WSLDistro     string
	RemoteDir     string
	Packages      []string
}

// LoadResumeMetadata reads target and requested package metadata for an
// existing session ID from SQLite.
func LoadResumeMetadata(ctx context.Context, storePath string, sessionID int64) (*ResumeMetadata, error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("ID de sessão inválido: %d", sessionID)
	}
	repo, err := store.Open(storePath)
	if err != nil {
		return nil, err
	}
	defer repo.Close()

	sessao, err := repo.ObterSessao(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	meta := &ResumeMetadata{
		TargetOS:      sessao.TargetOS,
		TargetVersion: sessao.TargetVersion,
		TargetArch:    sessao.TargetArch,
		FetchMethod:   sessao.FetchMethod,
		Image:         sessao.ImagemDocker,
		WSLDistro:     sessao.WSLDistro,
		RemoteDir:     sessao.RemoteDir,
	}
	if solicitados, listErr := repo.ListarPacotesSolicitados(ctx, sessionID); listErr == nil {
		for _, p := range solicitados {
			if p.Nome != "" {
				meta.Packages = append(meta.Packages, p.Nome)
			}
		}
	}
	return meta, nil
}

func (s *Service) newSession() *core.Sessao {
	return &core.Sessao{
		TargetOS:        s.cfg.TargetOS,
		TargetVersion:   s.cfg.TargetVersion,
		TargetArch:      s.cfg.TargetArch,
		FetchMethod:     s.cfg.FetchMethod,
		HostDestino:     s.cfg.SSH.Host,
		IncludeOptional: s.cfg.IncludeOptional,
		ConfigJSON:      s.configJSON(),
	}
}

func (s *Service) configJSON() string {
	payload := map[string]any{
		"target_os":        s.cfg.TargetOS,
		"target_version":   s.cfg.TargetVersion,
		"target_arch":      s.cfg.TargetArch,
		"version_report":   string(s.cfg.VersionReport),
		"packages":         s.cfg.Packages,
		"include_optional": s.cfg.IncludeOptional,
		"fetch_method":     s.cfg.FetchMethod,
		"image":            s.cfg.Image,
		"wsl_distro":       s.cfg.WSLDistro,
		"remote_dir":       s.cfg.RemoteDir,
		"ssh_host":         s.cfg.SSH.Host,
		"ssh_port":         s.cfg.SSH.Port,
		"ssh_user":         s.cfg.SSH.User,
		"install":          s.cfg.Install,
		"syslog_enabled":   s.cfg.SyslogEnabled,
		"syslog_host":      s.cfg.SyslogHost,
		"syslog_tag":       s.cfg.SyslogTag,
	}
	if jumps := SSHJumpMetadataFromConfigs(s.cfg.SSH.JumpHosts); len(jumps) > 0 {
		payload["ssh_jumps"] = jumps
	}
	if len(s.cfg.Repositories) > 0 {
		payload["repositories"] = s.cfg.Repositories
	}
	if s.cfg.ContainerHost != "" {
		payload["container_host"] = s.cfg.ContainerHost
	}
	if !s.cfg.Proxy.Empty() {
		payload["proxy"] = s.cfg.Proxy.Sanitized()
	}
	if s.cfg.OfflineBundlePath != "" {
		payload["offline_bundle_path"] = s.cfg.OfflineBundlePath
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func (s *Service) pipelineConfig(reporter core.PipelineReporter) core.PipelineConfig {
	if reporter == nil {
		reporter = s.cfg.Reporter
	}
	return core.PipelineConfig{
		Repo:              s.repo,
		Fetcher:           s.fetcher,
		Packages:          s.cfg.Packages,
		OutputBaseDir:     s.cfg.OutputBaseDir,
		StageTimeout:      s.cfg.StageTimeout,
		Logger:            s.cfg.Logger,
		Reporter:          reporter,
		EventSink:         s.eventSink,
		ForceReinstall:    s.cfg.ForceReinstall,
		Inspector:         s.inspect,
		Transfer:          s.transfer,
		SSH:               s.cfg.SSH,
		RemoteDir:         s.cfg.RemoteDir,
		Image:             s.cfg.Image,
		WSLDistro:         s.cfg.WSLDistro,
		Installer:         s.install,
		ConfirmInstall:    s.cfg.ConfirmInstall,
		OfflineBundlePath: s.cfg.OfflineBundlePath,
	}
}

// TargetFamily returns the package manager family for a target OS.
func TargetFamily(targetOS string) (string, error) {
	return familyForOS(strings.ToLower(strings.TrimSpace(targetOS)))
}

func familyForOS(targetOS string) (string, error) {
	switch targetOS {
	case "ubuntu", "debian":
		return "apt", nil
	case "centos", "rhel", "rockylinux", "almalinux", "fedora":
		return "rpm", nil
	case "alpine":
		return "apk", nil
	default:
		return "", fmt.Errorf("distro não suportada: %q", targetOS)
	}
}

func resolveFetchMethod(method, family string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	if method != "auto" {
		return method
	}
	if runtime.GOOS == "windows" && family == "apt" {
		return "wsl"
	}
	return "docker"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
