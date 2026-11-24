package resolver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"opf/internal/core"
)

var (
	apkPackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*$`)
	apkArchPattern    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// ApkResolver resolves Alpine dependencies with apk fetch --recursive and
// prepares isolated fetch commands inside the environment.
type ApkResolver struct {
	exec           core.CommandExecutor
	arch           string
	workDir        string
	allowUntrusted bool
	updated        bool
}

func NewApkResolver(exec core.CommandExecutor, arch, workDir string) *ApkResolver {
	return &ApkResolver{exec: exec, arch: arch, workDir: workDir}
}

// SetAllowUntrusted enables apk fetch --allow-untrusted. It is used only when
// the session explicitly opts out of signature verification for an additional
// repository.
func (r *ApkResolver) SetAllowUntrusted(value bool) {
	r.allowUntrusted = value
}

func (r *ApkResolver) ensureIndexes(ctx context.Context) error {
	if r.updated {
		return nil
	}
	cacheDir := r.workDir + "/apk-cache"
	for _, cmd := range [][]string{
		{"mkdir", "-p", cacheDir},
		{"apk", "--cache-dir", cacheDir, "update"},
	} {
		if _, err := r.exec.Exec(ctx, cmd); err != nil {
			return err
		}
	}
	r.updated = true
	return nil
}

func (r *ApkResolver) ListDependencies(ctx context.Context, pkg string, _ bool) ([]string, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	if _, err := core.ParsePackageSpec(core.RepositoryFamilyAPK, pkg); err != nil {
		return nil, fmt.Errorf("nome de pacote APK inválido: %q", pkg)
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	if err := r.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("preparar/resolver dependências APK de %q: %w", pkg, err)
	}

	cacheDir := r.workDir + "/apk-cache"
	fetchCmd := []string{"apk", "--cache-dir", cacheDir, "fetch"}
	if r.allowUntrusted {
		fetchCmd = append(fetchCmd, "--allow-untrusted")
	}
	fetchCmd = append(fetchCmd, "--recursive", "--simulate", "--arch="+r.arch, pkg)
	stdout, err := r.exec.Exec(ctx, fetchCmd)
	if err != nil {
		return nil, fmt.Errorf("preparar/resolver dependências APK de %q: %w", pkg, err)
	}

	deps, err := parseApkSimulate(stdout)
	if err != nil {
		return nil, fmt.Errorf("interpretar saída do apk fetch para %q: %w", pkg, err)
	}
	baseName := core.PackageBaseName(pkg)
	if core.PackagePinnedVersion(pkg) != "" {
		filtered := make([]string, 0, len(deps)+1)
		for _, dep := range deps {
			if core.PackageBaseName(dep) == baseName {
				continue
			}
			filtered = append(filtered, dep)
		}
		filtered = append(filtered, pkg)
		sort.Strings(filtered)
		deps = filtered
	} else if !contains(deps, pkg) {
		deps = append(deps, pkg)
		sort.Strings(deps)
	}
	return deps, nil
}

// ListCandidates reads candidate versions from an apk fetch simulation.
func (r *ApkResolver) ListCandidates(ctx context.Context, pkgs []string, _ bool) ([]core.CandidatePackage, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	if len(pkgs) == 0 {
		return nil, errors.New("nenhum pacote para resolver versões APK")
	}
	for _, pkg := range pkgs {
		if _, err := core.ParsePackageSpec(core.RepositoryFamilyAPK, pkg); err != nil {
			return nil, fmt.Errorf("nome de pacote APK inválido: %q", pkg)
		}
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	if err := r.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("resolver versões candidatas APK: %w", err)
	}
	cacheDir := r.workDir + "/apk-cache"
	fetchCmd := []string{"apk", "--cache-dir", cacheDir, "fetch", "--simulate", "--recursive", "--arch=" + r.arch}
	if r.allowUntrusted {
		fetchCmd = append(fetchCmd, "--allow-untrusted")
	}
	fetchCmd = append(fetchCmd, pkgs...)
	stdout, err := r.exec.Exec(ctx, fetchCmd)
	if err != nil {
		return nil, fmt.Errorf("resolver versões candidatas APK: %w", err)
	}
	versions, err := parseApkCandidates(stdout)
	if err != nil {
		return nil, err
	}
	candidates := make([]core.CandidatePackage, 0, len(pkgs))
	seen := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		base := core.PackageBaseName(pkg)
		if _, dup := seen[base]; dup {
			continue
		}
		seen[base] = struct{}{}
		version := core.PackagePinnedVersion(pkg)
		if version == "" {
			version = versions[base]
		}
		if version != "" {
			candidates = append(candidates, core.CandidatePackage{Name: base, Version: version})
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("nenhuma versão candidata APK encontrada")
	}
	return candidates, nil
}

func parseApkCandidates(output string) (map[string]string, error) {
	versions := make(map[string]string)
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		const prefix = "Downloading "
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		nvr := strings.TrimSuffix(strings.TrimSpace(line[idx+len(prefix):]), ".apk")
		name, version, err := apkNVR(nvr)
		if err != nil {
			continue
		}
		versions[name] = version
	}
	if len(versions) == 0 {
		return nil, errors.New("nenhuma versão candidata APK retornada")
	}
	return versions, nil
}

func apkNVR(nvr string) (string, string, error) {
	revision := strings.LastIndex(nvr, "-r")
	if revision <= 0 {
		return "", "", fmt.Errorf("versão APK inválida: %q", nvr)
	}
	withoutRevision := nvr[:revision]
	versionIdx := strings.LastIndex(withoutRevision, "-")
	if versionIdx <= 0 {
		return "", "", fmt.Errorf("nome/versão APK inválido: %q", nvr)
	}
	return withoutRevision[:versionIdx], withoutRevision[versionIdx+1:] + "-r" + nvr[revision+2:], nil
}

func (r *ApkResolver) DownloadCommands(pkgs []string) [][]string {
	if len(pkgs) == 0 {
		return nil
	}
	cacheDir := r.workDir + "/apk-cache"
	download := []string{"apk", "--cache-dir", cacheDir, "fetch"}
	if r.allowUntrusted {
		download = append(download, "--allow-untrusted")
	}
	download = append(download, "--recursive", "--arch="+r.arch, "--output="+r.workDir)
	download = append(download, pkgs...)
	return [][]string{
		{"mkdir", "-p", r.workDir, cacheDir},
		download,
	}
}

func (r *ApkResolver) validate() error {
	if !apkArchPattern.MatchString(r.arch) {
		return fmt.Errorf("arquitetura APK inválida: %q", r.arch)
	}
	if r.workDir == "" || !strings.HasPrefix(r.workDir, "/") {
		return fmt.Errorf("diretório isolado inválido: %q", r.workDir)
	}
	return nil
}

func parseApkSimulate(output string) ([]string, error) {
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		const prefix = "Downloading "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		nvr := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if strings.HasSuffix(nvr, ".apk") {
			nvr = strings.TrimSuffix(nvr, ".apk")
		}
		name, err := apkNameFromNVR(nvr)
		if err != nil {
			return nil, err
		}
		seen[name] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(seen) == 0 {
		return nil, errors.New("nenhuma dependência retornada")
	}
	deps := make([]string, 0, len(seen))
	for name := range seen {
		deps = append(deps, name)
	}
	sort.Strings(deps)
	return deps, nil
}

func apkNameFromNVR(nvr string) (string, error) {
	revisionIdx := strings.LastIndex(nvr, "-r")
	if revisionIdx <= 0 {
		return "", fmt.Errorf("versão APK inválida: %q", nvr)
	}
	revision := nvr[revisionIdx+2:]
	if _, err := strconv.Atoi(revision); err != nil {
		return "", fmt.Errorf("revisão APK inválida: %q", nvr)
	}
	withoutRevision := nvr[:revisionIdx]
	versionIdx := strings.LastIndex(withoutRevision, "-")
	if versionIdx <= 0 {
		return "", fmt.Errorf("nome/versão APK inválido: %q", nvr)
	}
	name := withoutRevision[:versionIdx]
	if !apkPackagePattern.MatchString(name) {
		return "", fmt.Errorf("nome de pacote APK inválido: %q", name)
	}
	return name, nil
}
