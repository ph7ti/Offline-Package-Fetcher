//go:build windows

package fetcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubuntu/gowsl"

	"opf/internal/core"
)

type WSLFetcher struct {
	options  wslOptions
	session  int64
	prepared bool
	workDir  string
	hostDir  string
	runner   *wslRunner
	resolver core.DependencyResolver
	reporter core.PipelineReporter
}

var _ core.PackageFetcher = (*WSLFetcher)(nil)
var _ core.MultiPackageFetcher = (*WSLFetcher)(nil)
var _ core.SessionBinder = (*WSLFetcher)(nil)
var _ core.ReporterSetter = (*WSLFetcher)(nil)

func NewWSLFetcher(opts ...WSLFetcherOption) *WSLFetcher {
	return &WSLFetcher{options: applyWSLFetcherOptions(opts)}
}

func (f *WSLFetcher) BindSession(id int64) error {
	if id <= 0 {
		return fmt.Errorf("id de sessão inválido: %d", id)
	}
	if f.prepared {
		return errors.New("não é possível trocar a sessão de um WSLFetcher já preparado")
	}
	if f.session != 0 && f.session != id {
		return fmt.Errorf("WSLFetcher já vinculado à sessão %d", f.session)
	}
	f.session = id
	return nil
}

func (f *WSLFetcher) Prepare(ctx context.Context, targetOS, targetVersion, arch string) (err error) {
	if f.prepared {
		return errors.New("ambiente WSL já preparado")
	}
	if f.session <= 0 {
		return errors.New("sessão não vinculada ao WSLFetcher; chame BindSession antes de Prepare")
	}
	targetOS = strings.ToLower(strings.TrimSpace(targetOS))
	targetVersion = strings.TrimSpace(targetVersion)
	kind, err := artifactKindForOS(targetOS)
	if err != nil {
		return err
	}
	if kind != artifactDeb {
		return fmt.Errorf("WSL F5 suporta apenas famílias APT; distro %q não é suportada", targetOS)
	}
	if f.options.resolverFactory == nil {
		return errors.New("resolver factory não configurada para WSL")
	}
	if _, err := dockerPlatform(arch); err != nil {
		return err
	}

	requestedDistro := strings.TrimSpace(f.options.distro)
	if requestedDistro == "" {
		requestedDistro = defaultWSLDistro(targetOS, targetVersion)
	}
	distroName, err := resolveWSLDistro(ctx, requestedDistro)
	if err != nil {
		return err
	}

	hostDir, err := os.MkdirTemp(f.options.tempDir, fmt.Sprintf("opf-wsl-%d-", f.session))
	if err != nil {
		return fmt.Errorf("criar diretório temporário no host: %w", err)
	}
	f.workDir = fmt.Sprintf("/tmp/opf-%d", f.session)
	f.hostDir = hostDir
	f.runner = &wslRunner{distro: distroName, workDir: f.workDir}

	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if cleanupErr := f.cleanup(cleanupCtx); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup após falha no prepare WSL: %w", cleanupErr))
		}
	}()

	osRelease, err := f.runner.Exec(ctx, []string{"cat", "/etc/os-release"})
	if err != nil {
		return fmt.Errorf("ler /etc/os-release da distro WSL: %w", err)
	}
	values := parseOSRelease(osRelease)
	if values["ID"] != targetOS {
		return fmt.Errorf("distro WSL %q tem ID %q, esperado %q", distroName, values["ID"], targetOS)
	}
	if values["VERSION_ID"] != targetVersion {
		return fmt.Errorf("distro WSL %q tem versão %q, esperada %q", distroName, values["VERSION_ID"], targetVersion)
	}

	stdout, err := f.runner.Exec(ctx, []string{"uname", "-m"})
	if err != nil {
		return fmt.Errorf("validar arquitetura da distro WSL: %w", err)
	}
	actualArch := strings.TrimSpace(stdout)
	if canonicalArch(actualArch) != canonicalArch(arch) {
		return fmt.Errorf("arquitetura da distro WSL (%q) difere da solicitada (%q)", actualArch, arch)
	}
	uid, err := f.runner.Exec(ctx, []string{"id", "-u"})
	if err != nil {
		return fmt.Errorf("verificar acesso root na distro WSL: %w", err)
	}
	if strings.TrimSpace(uid) != "0" {
		return errors.New("execução WSL não está como root")
	}

	if len(f.options.repositories) > 0 {
		if err := configureRepositories(ctx, f.runner, core.RepositoryFamilyAPT, f.session, f.options.repositories, f.options.logger); err != nil {
			return err
		}
	}

	f.resolver = f.options.resolverFactory(f.runner, arch, f.workDir)
	if f.resolver == nil {
		return errors.New("resolver factory WSL retornou nil")
	}
	f.prepared = true
	f.options.logger.Info("ambiente WSL pronto",
		"session_id", f.session,
		"distro", distroName,
		"workdir", f.workDir,
	)
	return nil
}

func (f *WSLFetcher) SetReporter(reporter core.PipelineReporter) {
	f.reporter = reporter
}

func (f *WSLFetcher) Download(ctx context.Context, pkg string, includeOptional bool) ([]core.PackageArtifact, error) {
	return f.DownloadPackages(ctx, []string{pkg}, includeOptional)
}

func (f *WSLFetcher) DownloadPackages(ctx context.Context, roots []string, includeOptional bool) ([]core.PackageArtifact, error) {
	if !f.prepared || f.runner == nil || f.resolver == nil {
		return nil, errors.New("ambiente WSL não preparado")
	}
	roots = uniqueNonEmpty(roots)
	if len(roots) == 0 {
		return nil, errors.New("nenhum pacote informado")
	}
	resolvedMap := make(map[string]struct{})
	for i, root := range roots {
		deps, err := f.resolver.ListDependencies(ctx, root, includeOptional)
		if err != nil {
			return nil, err
		}
		for _, dep := range deps {
			resolvedMap[dep] = struct{}{}
		}
		if f.reporter != nil {
			f.reporter.Progress("resolve", int64(i+1), int64(len(roots)))
		}
	}
	pkgs := make([]string, 0, len(resolvedMap))
	for name := range resolvedMap {
		pkgs = append(pkgs, name)
	}
	sort.Strings(pkgs)
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("nenhum pacote resolvido para %s", strings.Join(roots, ", "))
	}
	commands := f.resolver.DownloadCommands(pkgs)
	if len(commands) == 0 {
		return nil, errors.New("resolver não retornou comandos de download")
	}
	for _, cmd := range commands {
		if len(cmd) == 0 {
			continue
		}
		if _, err := f.runner.Exec(ctx, cmd); err != nil {
			return nil, fmt.Errorf("baixar pacotes na distro WSL: %w", err)
		}
	}

	inventory, err := f.runner.ExecScript(ctx, debInventoryScript)
	if err != nil {
		return nil, fmt.Errorf("verificar downloads na distro WSL (P1): %w", err)
	}
	files, err := parseDebInventory(inventory)
	if err != nil {
		return nil, err
	}
	downloaded := make(map[string]struct{}, len(files))
	for _, info := range files {
		downloaded[info.Name] = struct{}{}
	}
	for _, resolved := range pkgs {
		name := stripQualifier(resolved)
		if _, ok := downloaded[name]; !ok {
			return nil, fmt.Errorf("pacote resolvido sem artefato baixado: %q", resolved)
		}
	}

	rootSet := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		rootSet[stripQualifier(root)] = struct{}{}
	}
	artifacts := make([]core.PackageArtifact, 0, len(files))
	for i, info := range files {
		hostPath := filepath.Join(f.hostDir, info.File)
		if err := f.runner.CopyToHost(ctx, path.Join(f.workDir, info.File), hostPath); err != nil {
			return nil, err
		}
		hostHash, err := hashFile(hostPath)
		if err != nil {
			return nil, fmt.Errorf("calcular SHA-256 de %q no host: %w", info.File, err)
		}
		if !strings.EqualFold(hostHash, info.SHA256) {
			os.Remove(hostPath)
			return nil, fmt.Errorf("cópia corrompida do artefato %q", info.File)
		}
		_, isRoot := rootSet[info.Name]
		artifacts = append(artifacts, core.PackageArtifact{
			Name:         info.Name,
			Version:      info.Version,
			Arch:         info.Arch,
			LocalPath:    hostPath,
			IsDependency: !isRoot,
			SHA256:       strings.ToLower(hostHash),
			SHA256P1:     strings.ToLower(info.SHA256),
			SHA256P2:     strings.ToLower(hostHash),
		})
		if f.reporter != nil {
			f.reporter.Progress("copy", int64(i+1), int64(len(files)))
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	f.options.logger.Info("download concluído via WSL",
		"session_id", f.session,
		"packages", len(roots),
		"artifacts", len(artifacts),
	)
	return artifacts, nil
}

func (f *WSLFetcher) Cleanup(ctx context.Context) error {
	return f.cleanup(ctx)
}

func (f *WSLFetcher) cleanup(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cleanupTimeout)
		defer cancel()
	}
	var errs []error
	if f.runner != nil && len(f.options.repositories) > 0 {
		if err := cleanupRepositories(ctx, f.runner, core.RepositoryFamilyAPT, f.session, f.options.repositories); err != nil {
			errs = append(errs, err)
		}
	}
	if f.runner != nil && f.workDir != "" {
		if _, err := f.runner.ExecRaw(ctx, []string{"rm", "-rf", f.workDir}); err != nil {
			errs = append(errs, fmt.Errorf("remover diretório isolado WSL: %w", err))
		}
	}
	if f.hostDir != "" {
		if err := os.RemoveAll(f.hostDir); err != nil {
			errs = append(errs, fmt.Errorf("remover diretório temporário do host: %w", err))
		}
		f.hostDir = ""
	}
	f.prepared = false
	f.resolver = nil
	f.workDir = ""
	f.runner = nil
	return errors.Join(errs...)
}

func (f *WSLFetcher) HostDir() string {
	return f.hostDir
}

func (f *WSLFetcher) EnvironmentID() string {
	return f.options.distro + ":" + f.workDir
}

type wslRunner struct {
	distro  string
	workDir string
}

func (r *wslRunner) Exec(ctx context.Context, cmd []string) (string, error) {
	workDir := wslShellQuote(r.workDir)
	script := fmt.Sprintf("mkdir -p %s && cd %s && exec %s", workDir, workDir, wslShellJoin(cmd))
	output, err := runWSLScript(ctx, r.distro, script)
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func (r *wslRunner) ExecScript(ctx context.Context, script string) (string, error) {
	workDir := wslShellQuote(r.workDir)
	fullScript := fmt.Sprintf("mkdir -p %s && cd %s && %s", workDir, workDir, script)
	output, err := runWSLScript(ctx, r.distro, fullScript)
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func (r *wslRunner) ExecRaw(ctx context.Context, cmd []string) (string, error) {
	output, err := runWSLScript(ctx, r.distro, "exec "+wslShellJoin(cmd))
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func wslShellJoin(cmd []string) string {
	quoted := make([]string, 0, len(cmd))
	for _, arg := range cmd {
		quoted = append(quoted, wslShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func wslShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (r *wslRunner) CopyToHost(ctx context.Context, remotePath, hostPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(hostPath), ".wsl-copy-*")
	if err != nil {
		return fmt.Errorf("criar arquivo temporário para %q: %w", remotePath, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	var stderr strings.Builder
	cmdArgs := []string{"-d", r.distro, "-u", "root", "--", "cat", remotePath}
	cmd := exec.CommandContext(ctx, "wsl.exe", cmdArgs...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.Stdout = tmp
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tmp.Close()
		return fmt.Errorf("copiar %q da distro WSL: %w: %s", remotePath, err, truncate(stderr.String(), maxOutputLog))
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sincronizar cópia de %q: %w", remotePath, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("ajustar permissões de %q: %w", remotePath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechar cópia de %q: %w", remotePath, err)
	}
	if err := os.Rename(tmpName, hostPath); err != nil {
		return fmt.Errorf("publicar cópia de %q: %w", remotePath, err)
	}
	return nil
}

func runWSLScript(ctx context.Context, distro, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "-u", "root", "--", "sh")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("wsl.exe falhou: %w: %s", err, truncate(string(output), maxOutputLog))
	}
	return output, nil
}

func resolveWSLDistro(ctx context.Context, requested string) (string, error) {
	distros, err := gowsl.RegisteredDistros(ctx)
	if err != nil {
		return "", fmt.Errorf("listar distros WSL: %w", err)
	}
	names := make([]string, 0, len(distros))
	for _, distro := range distros {
		names = append(names, distro.Name())
		if distro.Name() == requested {
			return distro.Name(), nil
		}
	}
	for _, distro := range distros {
		if strings.EqualFold(distro.Name(), requested) {
			return distro.Name(), nil
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("distro WSL %q não registrada (nenhuma distro registrada)", requested)
	}
	return "", fmt.Errorf("distro WSL %q não registrada (registradas: %s)", requested, strings.Join(names, ", "))
}

func parseOSRelease(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(value, `"'`)
	}
	return values
}
