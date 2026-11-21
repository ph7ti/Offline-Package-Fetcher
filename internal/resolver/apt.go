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
}

func NewAptResolver(exec core.CommandExecutor, arch, workDir string) *AptResolver {
	return &AptResolver{
		exec:    exec,
		arch:    arch,
		workDir: workDir,
	}
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

	// Os índices APT precisam existir antes de apt-cache depends. O update é
	// feito com Dir::State::lists e Dir::Cache::archives isolados na sessão.
	prepare := [][]string{
		{"mkdir", "-p", r.listsDir() + "/partial", r.archivesDir() + "/partial"},
		append([]string{"apt-get"}, append(r.configArgs(), "update")...),
	}
	for _, cmd := range prepare {
		if _, err := r.exec.Exec(ctx, cmd); err != nil {
			return nil, fmt.Errorf("preparar índices APT isolados: %w", err)
		}
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
	if !contains(deps, pkg) {
		deps = append(deps, pkg)
	}
	return deps, nil
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
	if !packageNamePattern.MatchString(pkg) {
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
