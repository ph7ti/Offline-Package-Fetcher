package resolver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"opf/internal/core"
)

var (
	rpmPackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*$`)
	rpmArchPattern    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// YumDnfResolver resolves RPM dependencies with repoquery and prepares
// yumdownloader/dnf download commands inside the isolated environment.
type YumDnfResolver struct {
	exec            core.CommandExecutor
	arch            string
	workDir         string
	manager         string
	includeOptional bool
	roots           map[string]struct{}
}

func NewYumDnfResolver(exec core.CommandExecutor, arch, workDir string) *YumDnfResolver {
	return &YumDnfResolver{exec: exec, arch: arch, workDir: workDir, roots: make(map[string]struct{})}
}

func rpmNormalizeSpec(pkg string) (string, error) {
	spec, err := core.ParsePackageSpec(core.RepositoryFamilyRPM, pkg)
	if err != nil {
		return "", fmt.Errorf("nome de pacote RPM inválido: %q", pkg)
	}
	if spec.Version != "" {
		return spec.Name + "-" + spec.Version, nil
	}
	return spec.Name, nil
}

func (r *YumDnfResolver) ListDependencies(ctx context.Context, pkg string, includeOptional bool) ([]string, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	targetSpec, err := rpmNormalizeSpec(pkg)
	if err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}

	manager, err := r.detectManager(ctx)
	if err != nil {
		return nil, err
	}
	r.manager = manager
	r.includeOptional = includeOptional
	if r.roots == nil {
		r.roots = make(map[string]struct{})
	}
	r.roots[targetSpec] = struct{}{}
	if manager == "dnf" {
		// DNF resolves dependencies during download (--resolve --alldeps).
		// This also supports DNF5, which removed repoquery --resolve.
		return []string{targetSpec}, nil
	}

	cmd := r.listCommand(targetSpec)
	stdout, err := r.exec.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("resolver dependências RPM de %q: %w", pkg, err)
	}
	deps, err := parseRepoquery(stdout)
	if err != nil {
		return nil, fmt.Errorf("interpretar saída do repoquery para %q: %w", pkg, err)
	}
	if !contains(deps, targetSpec) {
		deps = append(deps, targetSpec)
		sort.Strings(deps)
	}
	return deps, nil
}

// ListCandidates reads candidate versions from repository metadata. For DNF,
// dependency names are also resolved through repoquery when available.
func (r *YumDnfResolver) ListCandidates(ctx context.Context, pkgs []string, includeOptional bool) ([]core.CandidatePackage, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	manager, err := r.detectManager(ctx)
	if err != nil {
		return nil, err
	}
	r.manager = manager
	r.includeOptional = includeOptional
	names := make(map[string]struct{}, len(pkgs))
	pinned := make(map[string]string)
	normalizedInputs := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		targetSpec, specErr := rpmNormalizeSpec(pkg)
		if specErr != nil {
			return nil, specErr
		}
		base := core.PackageBaseName(pkg)
		if ver := core.PackagePinnedVersion(pkg); ver != "" {
			pinned[base] = ver
		}
		names[base] = struct{}{}
		normalizedInputs = append(normalizedInputs, targetSpec)
	}
	if manager == "dnf" {
		depCmd := []string{"dnf", "repoquery", "--requires", "--resolve", "--recursive", "--queryformat=%{name}"}
		depCmd = append(depCmd, normalizedInputs...)
		if output, depErr := r.exec.Exec(ctx, depCmd); depErr == nil {
			for _, name := range strings.Split(output, "\n") {
				name = strings.TrimSpace(name)
				if rpmPackagePattern.MatchString(name) {
					names[name] = struct{}{}
				}
			}
		}
	}
	query := []string{"dnf", "repoquery", "--latest-limit=1", "--queryformat=%{name}\t%{epoch}:%{version}-%{release}"}
	if manager != "dnf" {
		query = []string{"repoquery", "--qf", "%{name}\t%{epoch}:%{version}-%{release}"}
	}
	for name := range names {
		query = append(query, name)
	}
	stdout, err := r.exec.Exec(ctx, query)
	if err != nil && manager == "dnf" {
		fallback := []string{"dnf", "repoquery", "--queryformat=%{name}\t%{epoch}:%{version}-%{release}"}
		for name := range names {
			fallback = append(fallback, name)
		}
		stdout, err = r.exec.Exec(ctx, fallback)
	}
	if err != nil {
		return nil, fmt.Errorf("resolver versões candidatas RPM: %w", err)
	}
	versions := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) != 2 {
			continue
		}
		versions[fields[0]] = fields[1]
	}
	candidates := make([]core.CandidatePackage, 0, len(names))
	for name := range names {
		version := pinned[name]
		if version == "" {
			version = versions[name]
		}
		if version != "" {
			candidates = append(candidates, core.CandidatePackage{Name: name, Version: version})
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("nenhuma versão candidata RPM encontrada")
	}
	return candidates, nil
}

func (r *YumDnfResolver) DownloadCommands(pkgs []string) [][]string {
	if r.manager == "" || len(pkgs) == 0 {
		return nil
	}

	var download []string
	switch r.manager {
	case "dnf":
		download = []string{"dnf"}
		if !r.includeOptional {
			download = append(download, "--setopt=install_weak_deps=False")
		}
		download = append(download,
			"download",
			"--resolve",
			"--alldeps",
			"--arch="+r.arch,
			"--destdir="+r.workDir,
		)
	case "yum":
		download = []string{
			"yumdownloader",
			"--resolve",
			"--archlist=" + r.arch,
			"--destdir=" + r.workDir,
		}
	default:
		return nil
	}
	if r.manager == "dnf" && len(r.roots) > 0 {
		roots := make([]string, 0, len(r.roots))
		for root := range r.roots {
			roots = append(roots, root)
		}
		sort.Strings(roots)
		download = append(download, roots...)
	} else {
		download = append(download, pkgs...)
	}

	return [][]string{
		{"mkdir", "-p", r.workDir},
		download,
	}
}

func (r *YumDnfResolver) detectManager(ctx context.Context) (string, error) {
	script := "if command -v dnf >/dev/null 2>&1; then echo dnf; " +
		"elif command -v yum >/dev/null 2>&1; then echo yum; " +
		"else exit 1; fi"
	stdout, err := r.exec.Exec(ctx, []string{"sh", "-c", script})
	if err != nil {
		return "", fmt.Errorf("detectar gerenciador RPM: %w", err)
	}
	manager := strings.TrimSpace(stdout)
	if manager != "dnf" && manager != "yum" {
		return "", fmt.Errorf("gerenciador RPM não suportado: %q", manager)
	}
	return manager, nil
}

func (r *YumDnfResolver) listCommand(pkg string) []string {
	switch r.manager {
	case "dnf":
		return []string{
			"dnf", "repoquery",
			"--requires", "--resolve", "--recursive",
			"--queryformat=%{name}",
			pkg,
		}
	default:
		return []string{
			"repoquery",
			"--requires", "--resolve", "--recursive",
			"--queryformat=%{name}",
			pkg,
		}
	}
}

func (r *YumDnfResolver) validate() error {
	if !rpmArchPattern.MatchString(r.arch) {
		return fmt.Errorf("arquitetura RPM inválida: %q", r.arch)
	}
	if r.workDir == "" || !strings.HasPrefix(r.workDir, "/") {
		return fmt.Errorf("diretório isolado inválido: %q", r.workDir)
	}
	return nil
}

func parseRepoquery(output string) ([]string, error) {
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.ContainsAny(line, " \t:") {
			continue
		}
		if !rpmPackagePattern.MatchString(line) {
			continue
		}
		seen[line] = struct{}{}
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
