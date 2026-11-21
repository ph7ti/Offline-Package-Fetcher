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

func (r *YumDnfResolver) ListDependencies(ctx context.Context, pkg string, includeOptional bool) ([]string, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	if !rpmPackagePattern.MatchString(pkg) {
		return nil, fmt.Errorf("nome de pacote RPM inválido: %q", pkg)
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
	r.roots[pkg] = struct{}{}
	if manager == "dnf" {
		// DNF resolves dependencies during download (--resolve --alldeps).
		// This also supports DNF5, which removed repoquery --resolve.
		return []string{pkg}, nil
	}

	cmd := r.listCommand(pkg)
	stdout, err := r.exec.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("resolver dependências RPM de %q: %w", pkg, err)
	}
	deps, err := parseRepoquery(stdout)
	if err != nil {
		return nil, fmt.Errorf("interpretar saída do repoquery para %q: %w", pkg, err)
	}
	if !contains(deps, pkg) {
		deps = append(deps, pkg)
		sort.Strings(deps)
	}
	return deps, nil
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
