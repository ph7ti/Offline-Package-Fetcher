package fetcher

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/go-sdk/client"
	"github.com/docker/go-sdk/container"
	cexec "github.com/docker/go-sdk/container/exec"
	"github.com/docker/go-sdk/container/wait"
	dockerclient "github.com/moby/moby/client"

	"opf/internal/core"
)

type artifactKind string

const (
	artifactDeb artifactKind = "deb"
	artifactRPM artifactKind = "rpm"
	artifactAPK artifactKind = "apk"
)

const (
	debInventoryScript = `set -eu
found=0
for f in *.deb; do
  [ -e "$f" ] || continue
  found=1
  dpkg-deb --info "$f" >/dev/null
  p=$(dpkg-deb --field "$f" Package)
  v=$(dpkg-deb --field "$f" Version)
  a=$(dpkg-deb --field "$f" Architecture)
  h=$(sha256sum -- "$f" | awk '{print $1}')
  printf '%s\t%s\t%s\t%s\t%s\n' "$f" "$p" "$v" "$a" "$h"
done
[ "$found" -eq 1 ] || { echo "nenhum .deb encontrado" >&2; exit 1; }
`
	rpmInventoryScript = `set -eu
found=0
for f in *.rpm; do
  [ -e "$f" ] || continue
  found=1
  rpm -K "$f" >/dev/null
  p=$(rpm -qp --qf '%{NAME}' "$f")
  v=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' "$f")
  a=$(rpm -qp --qf '%{ARCH}' "$f")
  h=$(sha256sum -- "$f" | awk '{print $1}')
  printf '%s\t%s\t%s\t%s\t%s\n' "$f" "$p" "$v" "$a" "$h"
done
[ "$found" -eq 1 ] || { echo "nenhum .rpm encontrado" >&2; exit 1; }
`
	apkInventoryScript = `set -eu
found=0
for f in *.apk; do
  [ -e "$f" ] || continue
  found=1
  apk verify "$f" >/dev/null 2>&1
  info=$(tar -xOf "$f" .PKGINFO 2>/dev/null)
  p=$(printf '%s\n' "$info" | awk -F' = ' '$1=="pkgname"{print $2; exit}')
  v=$(printf '%s\n' "$info" | awk -F' = ' '$1=="pkgver"{print $2; exit}')
  a=$(printf '%s\n' "$info" | awk -F' = ' '$1=="arch"{print $2; exit}')
  h=$(sha256sum -- "$f" | awk '{print $1}')
  printf '%s\t%s\t%s\t%s\t%s\n' "$f" "$p" "$v" "$a" "$h"
done
[ "$found" -eq 1 ] || { echo "nenhum .apk encontrado" >&2; exit 1; }
`
	cleanupTimeout       = 30 * time.Second
	maxOutputLog         = 4096
	maxArtifactCopyBytes = int64(2) << 30
)

var (
	sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	tagPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type ResolverFactory func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver

type Option func(*DockerFetcher)

func WithImage(image string) Option {
	return func(f *DockerFetcher) { f.imageOverride = image }
}

func WithTempDir(dir string) Option {
	return func(f *DockerFetcher) { f.tempDir = dir }
}

func WithLogger(logger *slog.Logger) Option {
	return func(f *DockerFetcher) { f.logger = logger }
}

func WithResolverFactory(factory ResolverFactory) Option {
	return func(f *DockerFetcher) { f.resolverFactory = factory }
}

func WithRepositories(repos []core.PackageRepository) Option {
	return func(f *DockerFetcher) { f.repositories = repos }
}

type runConfig struct {
	Image         string
	Platform      string
	ContainerName string
	WorkDir       string
}

type containerRuntime interface {
	ID() string
	Exec(ctx context.Context, cmd []string) (stdout string, err error)
	CopyFromContainer(ctx context.Context, path string) (io.ReadCloser, error)
	Terminate(ctx context.Context) error
}

type bulkArchiveCopier interface {
	CopyArchiveFromContainer(ctx context.Context, containerPath string) (io.ReadCloser, error)
}

type containerFactory func(ctx context.Context, cfg runConfig) (containerRuntime, error)

var (
	_ core.PackageFetcher      = (*DockerFetcher)(nil)
	_ core.MultiPackageFetcher = (*DockerFetcher)(nil)
	_ core.PreparedFetcher     = (*DockerFetcher)(nil)
	_ core.ReporterSetter      = (*DockerFetcher)(nil)
	_ core.StageObserverSetter = (*DockerFetcher)(nil)
)

// DockerFetcher implements core.PackageFetcher using an ephemeral Docker
// container built from the target distro image.
type DockerFetcher struct {
	resolverFactory ResolverFactory
	imageOverride   string
	tempDir         string
	logger          *slog.Logger
	factory         containerFactory
	repositories    []core.PackageRepository

	sessionID     int64
	prepared      bool
	targetOS      string
	targetVersion string
	targetArch    string
	image         string
	workDir       string
	hostDir       string
	containerID   string
	artifactKind  artifactKind
	reporter      core.PipelineReporter
	stageObserver core.StageObserver
	runtime       containerRuntime
	resolver      core.DependencyResolver
}

func NewDockerFetcher(opts ...Option) *DockerFetcher {
	f := &DockerFetcher{
		logger:  slog.Default(),
		factory: runDockerContainer,
	}
	for _, opt := range opts {
		opt(f)
	}
	if f.logger == nil {
		f.logger = slog.Default()
	}
	return f
}

func (f *DockerFetcher) BindSession(id int64) error {
	if id <= 0 {
		return fmt.Errorf("id de sessão inválido: %d", id)
	}
	if f.prepared {
		return errors.New("não é possível trocar a sessão de um fetcher já preparado")
	}
	if f.sessionID != 0 && f.sessionID != id {
		return fmt.Errorf("fetcher já vinculado à sessão %d", f.sessionID)
	}
	f.sessionID = id
	return nil
}

func (f *DockerFetcher) Prepare(ctx context.Context, targetOS, targetVersion, arch string) (err error) {
	if f.prepared {
		return errors.New("ambiente já preparado")
	}
	if f.sessionID <= 0 {
		return errors.New("sessão não vinculada ao fetcher; chame BindSession antes de Prepare")
	}

	targetOS = strings.ToLower(strings.TrimSpace(targetOS))
	targetVersion = strings.TrimSpace(targetVersion)
	arch = strings.TrimSpace(arch)

	kind, err := artifactKindForOS(targetOS)
	if err != nil {
		return err
	}
	if !tagPattern.MatchString(targetVersion) {
		return fmt.Errorf("versão de destino inválida: %q", targetVersion)
	}
	platform, err := dockerPlatform(arch)
	if err != nil {
		return err
	}
	if f.resolverFactory == nil {
		return errors.New("resolver factory não configurada")
	}

	image := f.imageOverride
	if image == "" {
		image = targetOS + ":" + targetVersion
	}

	hostDir, err := os.MkdirTemp(f.tempDir, fmt.Sprintf("opf-%d-", f.sessionID))
	if err != nil {
		return fmt.Errorf("criar diretório temporário no host: %w", err)
	}

	f.targetOS = targetOS
	f.targetVersion = targetVersion
	f.targetArch = arch
	f.image = image
	f.artifactKind = kind
	f.workDir = fmt.Sprintf("/tmp/opf-%d", f.sessionID)
	f.hostDir = hostDir

	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if cleanupErr := f.cleanup(cleanupCtx); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup após falha no prepare: %w", cleanupErr))
		}
	}()

	containerName := fmt.Sprintf("opf-%d-%d", f.sessionID, time.Now().UnixNano())
	f.logger.Info("preparando ambiente isolado",
		"session_id", f.sessionID,
		"image", image,
		"platform", platform,
		"workdir", f.workDir,
	)

	runtime, err := f.factory(ctx, runConfig{
		Image:         image,
		Platform:      platform,
		ContainerName: containerName,
		WorkDir:       f.workDir,
	})
	if err != nil {
		return fmt.Errorf("criar contêiner Docker: %w", err)
	}
	f.runtime = runtime
	f.containerID = runtime.ID()

	stdout, err := runtime.Exec(ctx, []string{"uname", "-m"})
	if err != nil {
		return fmt.Errorf("validar arquitetura do ambiente isolado: %w", err)
	}
	actualArch := strings.TrimSpace(stdout)
	if canonicalArch(actualArch) != canonicalArch(arch) {
		return fmt.Errorf("arquitetura do contêiner (%q) difere da solicitada (%q)", actualArch, arch)
	}

	if kind == artifactRPM {
		if err := f.prepareRPMTools(ctx); err != nil {
			return err
		}
	}

	if len(f.repositories) > 0 {
		runner := dockerRepositoryRunner{runtime: f.runtime}
		if err := configureRepositories(ctx, runner, repositoryFamilyForKind(kind), f.sessionID, f.repositories, f.logger); err != nil {
			return err
		}
	}

	f.resolver = f.resolverFactory(runtime, arch, f.workDir)
	if f.resolver == nil {
		return errors.New("resolver factory retornou nil")
	}
	applyRepositoryPolicy(f.resolver, kind, f.repositories)

	f.prepared = true
	f.logger.Info("ambiente isolado pronto",
		"session_id", f.sessionID,
		"container_id", runtime.ID(),
	)
	return nil
}

const centosStoreBase = "https://mirror.nsc.liu.se/centos-store"

func (f *DockerFetcher) prepareRPMTools(ctx context.Context) error {
	if f.runtime == nil {
		return errors.New("runtime RPM não preparado")
	}
	if f.targetOS == "centos" && f.targetVersion == "7" {
		configure := "sed -i " +
			"-e 's/^mirrorlist=/#mirrorlist=/' " +
			"-e 's|^#baseurl=http://mirror.centos.org/centos/|baseurl=" + centosStoreBase + "/|' " +
			"-e 's|\\$releasever|7.9.2009|g' /etc/yum.repos.d/CentOS-*.repo"
		if _, err := f.runtime.Exec(ctx, []string{"sh", "-c", configure}); err != nil {
			return fmt.Errorf("configurar repositórios do CentOS 7: %w", err)
		}
	}
	importKeys := `for key in /etc/pki/rpm-gpg/*; do [ -f "$key" ] && rpm --import "$key" 2>/dev/null || true; done`
	if _, err := f.runtime.Exec(ctx, []string{"sh", "-c", importKeys}); err != nil {
		return fmt.Errorf("importar chaves RPM no ambiente isolado: %w", err)
	}
	install := "if command -v dnf >/dev/null 2>&1; then dnf install -y dnf-plugins-core; " +
		"elif command -v yum >/dev/null 2>&1; then yum install -y yum-utils; " +
		"else echo 'dnf/yum não encontrado' >&2; exit 1; fi"
	if _, err := f.runtime.Exec(ctx, []string{"sh", "-c", install}); err != nil {
		return fmt.Errorf("instalar ferramentas RPM no ambiente isolado: %w", err)
	}
	if f.supportsEPEL() {
		if err := f.enableEPEL(ctx); err != nil {
			f.logger.Warn("habilitar EPEL no ambiente isolado falhou; pacotes de repositórios adicionais podem não ser encontrados",
				"target", f.targetOS+":"+f.targetVersion, "erro", err)
		}
	}
	return nil
}

// supportsEPEL reports whether the target is an Enterprise Linux derivative
// that benefits from the Extra Packages for Enterprise Linux repository.
// Fedora already ships the same packages in its own repositories.
func (f *DockerFetcher) supportsEPEL() bool {
	switch f.targetOS {
	case "rockylinux", "almalinux", "centos", "rhel":
		return true
	default:
		return false
	}
}

// enableEPEL installs epel-release, falling back to the Fedora-hosted release
// RPM when the distribution repositories do not offer it (notably UBI). The
// operation is best-effort: failure is logged and the base repositories keep
// working for packages that do not need EPEL.
func (f *DockerFetcher) enableEPEL(ctx context.Context) error {
	install := "if command -v dnf >/dev/null 2>&1; then dnf install -y epel-release; " +
		"else yum install -y epel-release; fi"
	if _, err := f.runtime.Exec(ctx, []string{"sh", "-c", install}); err != nil {
		releaseURL, ok := epelReleaseURL(f.targetVersion)
		if !ok {
			return fmt.Errorf("epel-release indisponível para %s: %w", f.targetVersion, err)
		}
		fallback := "if command -v dnf >/dev/null 2>&1; then dnf install -y " + releaseURL + "; " +
			"else yum install -y " + releaseURL + "; fi"
		if _, fallbackErr := f.runtime.Exec(ctx, []string{"sh", "-c", fallback}); fallbackErr != nil {
			return errors.Join(err, fallbackErr)
		}
	}
	// Several EPEL packages depend on CodeReady Builder (EL9) or PowerTools
	// (EL8). Enable whichever repository exists; absence is not fatal.
	enableCRB := "if command -v dnf >/dev/null 2>&1; then " +
		"dnf config-manager --set-enabled crb >/dev/null 2>&1 || " +
		"dnf config-manager --set-enabled powertools >/dev/null 2>&1 || true; fi"
	_, _ = f.runtime.Exec(ctx, []string{"sh", "-c", enableCRB})
	// epel-release adds its own GPG key after the generic import done in
	// prepareRPMTools. Import it again so rpm -K validates EPEL artifacts.
	importKeys := `for key in /etc/pki/rpm-gpg/*; do [ -f "$key" ] && rpm --import "$key" 2>/dev/null || true; done`
	_, _ = f.runtime.Exec(ctx, []string{"sh", "-c", importKeys})
	return nil
}

func epelReleaseURL(targetVersion string) (string, bool) {
	major := strings.SplitN(strings.TrimSpace(targetVersion), ".", 2)[0]
	switch major {
	case "7", "8", "9":
		return "https://dl.fedoraproject.org/pub/epel/epel-release-latest-" + major + ".noarch.rpm", true
	default:
		return "", false
	}
}

func isMissingPackageError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "No package") || strings.Contains(message, "No matching Packages")
}
func (f *DockerFetcher) SetReporter(reporter core.PipelineReporter) {
	f.reporter = reporter
}

func (f *DockerFetcher) SetStageObserver(observer core.StageObserver) {
	f.stageObserver = observer
}

// IsPrepared reports whether the isolated Docker environment is already active.
func (f *DockerFetcher) IsPrepared() bool {
	return f.prepared && f.runtime != nil && f.resolver != nil
}

func (f *DockerFetcher) Download(ctx context.Context, pkg string, includeOptional bool) ([]core.PackageArtifact, error) {
	return f.DownloadPackages(ctx, []string{pkg}, includeOptional)
}

func (f *DockerFetcher) ResolveCandidates(ctx context.Context, roots []string, includeOptional bool) ([]core.CandidatePackage, error) {
	if !f.prepared || f.runtime == nil || f.resolver == nil {
		return nil, errors.New("ambiente não preparado")
	}
	return resolveCandidates(ctx, f.resolver, roots, includeOptional)
}

func (f *DockerFetcher) DownloadPackages(ctx context.Context, roots []string, includeOptional bool) ([]core.PackageArtifact, error) {
	if !f.prepared || f.runtime == nil || f.resolver == nil {
		return nil, errors.New("ambiente não preparado")
	}
	roots = uniqueNonEmpty(roots)
	if len(roots) == 0 {
		return nil, errors.New("nenhum pacote informado")
	}

	resolved := make(map[string]struct{})
	for i, root := range roots {
		deps, err := f.resolver.ListDependencies(ctx, root, includeOptional)
		if err != nil {
			return nil, err
		}
		for _, dep := range deps {
			resolved[dep] = struct{}{}
		}
		if f.reporter != nil {
			f.reporter.Progress("resolve", int64(i+1), int64(len(roots)))
		}
	}
	pkgs := make([]string, 0, len(resolved))
	for name := range resolved {
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
	if f.stageObserver != nil {
		f.stageObserver(5)
	}
	for _, cmd := range commands {
		if len(cmd) == 0 {
			continue
		}
		if _, err := f.runtime.Exec(ctx, cmd); err != nil {
			hint := ""
			if isMissingPackageError(err) {
				hint = fmt.Sprintf(" (pacote indisponível para %s:%s; confira o nome usado pela distro de destino e os repositórios adicionais)", f.targetOS, f.targetVersion)
			}
			return nil, fmt.Errorf("baixar pacotes no ambiente isolado%s: %w", hint, err)
		}
	}

	// P1: valida a integridade dos pacotes e coleta metadados/hashes ainda
	// dentro do ambiente isolado. Nada é copiado antes desta etapa.
	inventoryScript := debInventoryScript
	parseInventory := parseDebInventory
	switch f.artifactKind {
	case artifactRPM:
		inventoryScript = rpmInventoryScript
		parseInventory = parseRPMInventory
	case artifactAPK:
		inventoryScript = apkInventoryScript
		parseInventory = parseAPKInventory
	}
	inventory, err := f.runtime.Exec(ctx, []string{"sh", "-c", inventoryScript})
	if err != nil {
		return nil, fmt.Errorf("verificar downloads no ambiente (P1): %w", err)
	}
	files, err := parseInventory(inventory)
	if err != nil {
		return nil, err
	}

	downloaded := make(map[string]struct{}, len(files))
	for _, info := range files {
		downloaded[info.Name] = struct{}{}
	}
	if f.artifactKind == artifactRPM {
		for _, root := range roots {
			name := stripQualifier(root)
			if _, ok := downloaded[name]; !ok {
				return nil, fmt.Errorf("pacote principal sem artefato baixado: %q", root)
			}
		}
	} else {
		for _, resolved := range pkgs {
			name := stripQualifier(resolved)
			if _, ok := downloaded[name]; !ok {
				return nil, fmt.Errorf("pacote resolvido sem artefato baixado: %q", resolved)
			}
		}
	}

	rootSet := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		rootSet[stripQualifier(root)] = struct{}{}
	}
	var artifacts []core.PackageArtifact
	if copier, ok := f.runtime.(bulkArchiveCopier); ok {
		artifacts, err = f.copyFilesFromArchive(ctx, copier, files, rootSet)
		if err != nil {
			return nil, err
		}
	} else {
		artifacts = make([]core.PackageArtifact, 0, len(files))
		for i, info := range files {
			hostPath := filepath.Join(f.hostDir, info.File)
			if err := f.copyFileFromContainer(ctx, info.File, hostPath); err != nil {
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
	}

	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	f.logger.Info("download concluído no ambiente isolado",
		"session_id", f.sessionID,
		"packages", len(roots),
		"artifacts", len(artifacts),
	)
	return artifacts, nil
}

func (f *DockerFetcher) Cleanup(ctx context.Context) error {
	return f.cleanup(ctx)
}

func (f *DockerFetcher) cleanup(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cleanupTimeout)
		defer cancel()
	}

	var errs []error
	if f.runtime != nil {
		if err := f.runtime.Terminate(ctx); err != nil && !errdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("remover contêiner: %w", err))
		}
		f.runtime = nil
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
	f.image = ""
	return errors.Join(errs...)
}

func (f *DockerFetcher) ContainerID() string {
	return f.containerID
}

func (f *DockerFetcher) HostDir() string {
	return f.hostDir
}

func (f *DockerFetcher) EnvironmentID() string {
	return f.containerID
}

func (f *DockerFetcher) copyFilesFromArchive(ctx context.Context, copier bulkArchiveCopier, files []artifactInfo, rootSet map[string]struct{}) ([]core.PackageArtifact, error) {
	stream, err := copier.CopyArchiveFromContainer(ctx, f.workDir)
	if err != nil {
		return nil, fmt.Errorf("copiar artefatos do contêiner em lote: %w", err)
	}
	defer stream.Close()

	expected := make(map[string]artifactInfo, len(files))
	for _, info := range files {
		expected[info.File] = info
	}
	workDirBase := path.Base(path.Clean(f.workDir))

	tr := tar.NewReader(stream)
	extracted := make(map[string]struct{}, len(files))
	artifacts := make([]core.PackageArtifact, 0, len(files))

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ler arquivo tar do contêiner: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		clean := path.Clean(strings.ReplaceAll(header.Name, `\`, "/"))
		if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		dir := path.Dir(clean)
		if dir != "." && dir != workDirBase {
			continue
		}
		base := path.Base(clean)
		info, ok := expected[base]
		if !ok {
			continue
		}
		if _, dup := extracted[info.File]; dup {
			return nil, fmt.Errorf("artefato duplicado no tar do contêiner: %q", info.File)
		}

		hostPath := filepath.Join(f.hostDir, info.File)
		if err := writeLimitedFile(f.hostDir, hostPath, tr, maxArtifactCopyBytes); err != nil {
			return nil, fmt.Errorf("extrair %q do contêiner: %w", info.File, err)
		}
		hostHash, err := hashFile(hostPath)
		if err != nil {
			return nil, fmt.Errorf("calcular SHA-256 de %q no host: %w", info.File, err)
		}
		if !strings.EqualFold(hostHash, info.SHA256) {
			os.Remove(hostPath)
			return nil, fmt.Errorf("cópia corrompida do artefato %q", info.File)
		}

		extracted[info.File] = struct{}{}
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
			f.reporter.Progress("copy", int64(len(extracted)), int64(len(files)))
		}
	}

	if len(extracted) != len(files) {
		for _, info := range files {
			if _, ok := extracted[info.File]; !ok {
				return nil, fmt.Errorf("artefato %q ausente do tar copiado do contêiner", info.File)
			}
		}
	}
	return artifacts, nil
}

func writeLimitedFile(tempBase, hostPath string, src io.Reader, limit int64) error {
	tmp, err := os.CreateTemp(tempBase, ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	written, err := io.Copy(tmp, io.LimitReader(src, limit+1))
	if err != nil {
		tmp.Close()
		return err
	}
	if written > limit {
		tmp.Close()
		return fmt.Errorf("artefato excede o limite de %d bytes", limit)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, hostPath)
}

func (f *DockerFetcher) copyFileFromContainer(ctx context.Context, name, hostPath string) error {
	reader, err := f.runtime.CopyFromContainer(ctx, path.Join(f.workDir, name))
	if err != nil {
		return fmt.Errorf("copiar %q do contêiner: %w", name, err)
	}
	defer reader.Close()
	if err := writeLimitedFile(f.hostDir, hostPath, reader, maxArtifactCopyBytes); err != nil {
		return fmt.Errorf("copiar conteúdo de %q: %w", name, err)
	}
	return nil
}

type artifactInfo struct {
	File    string
	Name    string
	Version string
	Arch    string
	SHA256  string
}

func parseDebInventory(output string) ([]artifactInfo, error) {
	return parseArtifactInventory(output, ".deb")
}

func parseRPMInventory(output string) ([]artifactInfo, error) {
	return parseArtifactInventory(output, ".rpm")
}

func parseAPKInventory(output string) ([]artifactInfo, error) {
	return parseArtifactInventory(output, ".apk")
}

func parseArtifactInventory(output, extension string) ([]artifactInfo, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, errors.New("inventário de downloads vazio")
	}

	infos := make([]artifactInfo, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("linha de inventário inválida: %q", line)
		}
		info := artifactInfo{
			File:    fields[0],
			Name:    fields[1],
			Version: fields[2],
			Arch:    fields[3],
			SHA256:  fields[4],
		}
		if info.File == "" || filepath.Base(info.File) != info.File || !strings.HasSuffix(info.File, extension) {
			return nil, fmt.Errorf("nome de artefato inválido no inventário: %q", info.File)
		}
		if info.Name == "" || info.Version == "" || info.Arch == "" {
			return nil, fmt.Errorf("metadados incompletos para %q", info.File)
		}
		if !sha256Pattern.MatchString(info.SHA256) {
			return nil, fmt.Errorf("SHA-256 inválido para %q", info.File)
		}
		if _, ok := seen[info.File]; ok {
			return nil, fmt.Errorf("artefato duplicado no inventário: %q", info.File)
		}
		seen[info.File] = struct{}{}
		info.SHA256 = strings.ToLower(info.SHA256)
		infos = append(infos, info)
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("nenhum artefato %s baixado", extension)
	}
	return infos, nil
}

func stripQualifier(pkg string) string {
	if idx := strings.IndexByte(pkg, ':'); idx >= 0 {
		return pkg[:idx]
	}
	return pkg
}

func uniqueNonEmpty(values []string) []string {
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

func artifactKindForOS(targetOS string) (artifactKind, error) {
	switch targetOS {
	case "ubuntu", "debian":
		return artifactDeb, nil
	case "centos", "rhel", "rockylinux", "almalinux", "fedora":
		return artifactRPM, nil
	case "alpine":
		return artifactAPK, nil
	default:
		return "", fmt.Errorf("distro %q não suportada (use ubuntu/debian, centos/rhel/rockylinux/almalinux/fedora ou alpine)", targetOS)
	}
}

func canonicalArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "armhf", "armv7", "armv7l":
		return "armhf"
	case "i386", "i686", "x86":
		return "i386"
	case "ppc64el", "ppc64le":
		return "ppc64el"
	case "s390x":
		return "s390x"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

func dockerPlatform(arch string) (string, error) {
	switch canonicalArch(arch) {
	case "amd64":
		return "linux/amd64", nil
	case "arm64":
		return "linux/arm64", nil
	case "armhf":
		return "linux/arm/v7", nil
	case "i386":
		return "linux/386", nil
	case "ppc64el":
		return "linux/ppc64le", nil
	case "s390x":
		return "linux/s390x", nil
	default:
		return "", fmt.Errorf("arquitetura de destino não suportada: %q", arch)
	}
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func runDockerContainer(ctx context.Context, cfg runConfig) (containerRuntime, error) {
	startCommand := fmt.Sprintf("mkdir -p %s && exec sleep 2147483647", cfg.WorkDir)
	ctr, err := container.Run(ctx,
		container.WithImage(cfg.Image),
		container.WithImagePlatform(cfg.Platform),
		container.WithName(cfg.ContainerName),
		container.WithCmd("sh", "-c", startCommand),
		container.WithWaitStrategy(wait.ForExec([]string{"sh", "-c", "test -d " + cfg.WorkDir})),
	)
	if err != nil {
		cleanupContainerByName(cfg.ContainerName)
		return nil, err
	}
	return &dockerRuntime{ctr: ctr, workDir: cfg.WorkDir}, nil
}

func cleanupContainerByName(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	cli, err := client.New(ctx)
	if err != nil {
		return
	}
	summary, err := cli.FindContainerByName(ctx, name)
	if err != nil || summary == nil {
		return
	}
	_, _ = cli.ContainerRemove(ctx, summary.ID, dockerclient.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
}

type dockerRuntime struct {
	ctr     *container.Container
	workDir string
}

func (r *dockerRuntime) ID() string {
	return r.ctr.ID()
}

func (r *dockerRuntime) Exec(ctx context.Context, cmd []string) (string, error) {
	exitCode, reader, err := r.ctr.Exec(ctx, cmd,
		cexec.Multiplexed(),
		cexec.WithWorkingDir(r.workDir),
	)
	if err != nil {
		return "", fmt.Errorf("executar %q: %w", formatCommand(cmd), err)
	}

	var output string
	if reader != nil {
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			return "", fmt.Errorf("ler saída de %q: %w", formatCommand(cmd), readErr)
		}
		output = string(data)
	}
	if exitCode != 0 {
		return output, fmt.Errorf("comando %q falhou (exit %d): %s",
			formatCommand(cmd), exitCode, truncate(output, maxOutputLog))
	}
	return output, nil
}

func (r *dockerRuntime) CopyFromContainer(ctx context.Context, path string) (io.ReadCloser, error) {
	return r.ctr.CopyFromContainer(ctx, path)
}

func (r *dockerRuntime) CopyArchiveFromContainer(ctx context.Context, containerPath string) (io.ReadCloser, error) {
	res, err := r.ctr.Client().CopyFromContainer(ctx, r.ctr.ID(), dockerclient.CopyFromContainerOptions{
		SourcePath: containerPath,
	})
	if err != nil {
		return nil, err
	}
	return res.Content, nil
}

func (r *dockerRuntime) Terminate(ctx context.Context) error {
	return r.ctr.Terminate(ctx, container.TerminateTimeout(cleanupTimeout))
}

func formatCommand(cmd []string) string {
	return strings.Join(cmd, " ")
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "... (truncado)"
}
