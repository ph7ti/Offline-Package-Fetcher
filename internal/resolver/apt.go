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
	packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(?::[a-z0-9][a-z0-9-]*)?$`)
	archPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// AptResolver resolves Debian/Ubuntu dependencies and builds download commands
// that run inside the isolated environment.
type AptResolver struct {
	exec    core.CommandExecutor
	arch    string
	workDir string
	updated bool
}

func NewAptResolver(exec core.CommandExecutor, arch, workDir string) *AptResolver {
	return &AptResolver{
		exec:    exec,
		arch:    arch,
		workDir: workDir,
	}
}

func (r *AptResolver) ensureIndexes(ctx context.Context) error {
	if r.updated {
		return nil
	}
	// Os índices APT precisam existir antes de apt-cache depends/policy. O update é
	// feito com Dir::State::lists e Dir::Cache::archives isolados na sessão e
	// reutilizado nas chamadas seguintes dentro do mesmo ambiente.
	prepare := [][]string{
		{"mkdir", "-p", r.listsDir() + "/partial", r.archivesDir() + "/partial"},
		append([]string{"apt-get"}, append(r.configArgs(), "update")...),
	}
	for _, cmd := range prepare {
		if _, err := r.exec.Exec(ctx, cmd); err != nil {
			return err
		}
	}
	r.updated = true
	return nil
}

func (r *AptResolver) ListDependencies(ctx context.Context, pkg string, includeOptional bool) ([]string, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	if err := r.validatePackage(pkg); err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}

	if err := r.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("preparar índices APT isolados: %w", err)
	}

	cmd := append([]string{"apt-cache"}, r.configArgs()...)
	cmd = append(cmd, "depends", "--recurse",
		"--no-conflicts", "--no-breaks", "--no-replaces")
	if !includeOptional {
		cmd = append(cmd, "--no-recommends", "--no-suggests", "--no-enhances")
	}
	cmd = append(cmd, pkg)

	stdout, err := r.exec.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("resolver dependências APT de %q: %w", pkg, err)
	}

	deps, err := parseAptCacheDepends(stdout)
	if err != nil {
		return nil, fmt.Errorf("interpretar saída do apt-cache para %q: %w", pkg, err)
	}
	baseName := core.PackageBaseName(pkg)
	if core.PackagePinnedVersion(pkg) != "" {
		// Replace unpinned root name with the pinned 'pkg=version' spec so
		// apt-get download fetches the exact requested version.
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
	}
	return deps, nil
}

// ListCandidates reads candidate versions from the isolated APT indexes
// without downloading any package.
func (r *AptResolver) ListCandidates(ctx context.Context, pkgs []string, _ bool) ([]core.CandidatePackage, error) {
	if r.exec == nil {
		return nil, errors.New("executor do ambiente isolado não configurado")
	}
	if len(pkgs) == 0 {
		return nil, errors.New("nenhum pacote para resolver versões APT")
	}
	pinned := make(map[string]string)
	queryNames := make([]string, 0, len(pkgs))
	seen := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		if err := r.validatePackage(pkg); err != nil {
			return nil, err
		}
		base := core.PackageBaseName(pkg)
		if ver := core.PackagePinnedVersion(pkg); ver != "" {
			pinned[base] = ver
		}
		if _, ok := seen[base]; !ok {
			seen[base] = struct{}{}
			queryNames = append(queryNames, base)
		}
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	if err := r.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("preparar índices APT para versões candidatas: %w", err)
	}
	cmd := append([]string{"apt-cache"}, r.configArgs()...)
	cmd = append(cmd, "policy")
	cmd = append(cmd, queryNames...)
	stdout, err := r.exec.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("resolver versões candidatas APT: %w", err)
	}
	versions := parseAptPolicy(stdout)
	candidates := make([]core.CandidatePackage, 0, len(queryNames))
	for _, base := range queryNames {
		version := pinned[base]
		if version == "" {
			version = versions[base]
		}
		if version == "" {
			continue
		}
		candidates = append(candidates, core.CandidatePackage{Name: base, Version: version})
	}
	if len(candidates) == 0 {
		return nil, errors.New("nenhuma versão candidata APT encontrada")
	}
	return candidates, nil
}

func parseAptPolicy(output string) map[string]string {
	versions := make(map[string]string)
	var current string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			current = strings.TrimSuffix(line, ":")
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(line, "Candidate:") {
			version := strings.TrimSpace(strings.TrimPrefix(line, "Candidate:"))
			if version != "" && version != "(none)" {
				versions[current] = version
			}
		}
	}
	return versions
}

func (r *AptResolver) DownloadCommands(pkgs []string) [][]string {
	download := append([]string{"apt-get"}, r.configArgs()...)
	download = append(download, "-o", "APT::Architecture="+r.arch, "download")
	download = append(download, pkgs...)

	return [][]string{
		{"mkdir", "-p", r.listsDir() + "/partial", r.archivesDir() + "/partial"},
		download,
	}
}

func (r *AptResolver) configArgs() []string {
	return []string{
		"-o", "Dir::State::lists=" + r.listsDir(),
		"-o", "Dir::Cache::archives=" + r.archivesDir(),
		"-o", "Dir::Cache::pkgcache=" + r.workDir + "/pkgcache.bin",
		"-o", "Dir::Cache::srcpkgcache=" + r.workDir + "/srcpkgcache.bin",
		"-o", "Acquire::Retries=3",
	}
}

func (r *AptResolver) listsDir() string {
	return r.workDir + "/lists"
}

func (r *AptResolver) archivesDir() string {
	return r.workDir + "/archives"
}

func (r *AptResolver) validate() error {
	if !archPattern.MatchString(r.arch) {
		return fmt.Errorf("arquitetura APT inválida: %q", r.arch)
	}
	if r.workDir == "" || !strings.HasPrefix(r.workDir, "/") {
		return fmt.Errorf("diretório isolado inválido: %q", r.workDir)
	}
	return nil
}

func (r *AptResolver) validatePackage(pkg string) error {
	if _, err := core.ParsePackageSpec(core.RepositoryFamilyAPT, pkg); err != nil {
		return fmt.Errorf("nome de pacote APT inválido: %q", pkg)
	}
	return nil
}

func parseAptCacheDepends(output string) ([]string, error) {
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}

		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "W:") || strings.HasPrefix(name, "N:") || strings.HasPrefix(name, "E:") {
			continue
		}
		if strings.HasPrefix(name, "<") && strings.HasSuffix(name, ">") {
			continue
		}
		if !packageNamePattern.MatchString(name) {
			return nil, fmt.Errorf("linha inesperada na saída do apt-cache: %q", name)
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

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
