package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type fakeExec struct {
	output string
	err    error
	calls  [][]string
}

func (f *fakeExec) Exec(_ context.Context, cmd []string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), cmd...))
	return f.output, f.err
}

func loadVimFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "test", "fixtures", "apt-cache-depends-vim-22.04.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ler fixture: %v", err)
	}
	return string(data)
}

func TestAptResolverListDependencies(t *testing.T) {
	exec := &fakeExec{output: loadVimFixture(t)}
	r := NewAptResolver(exec, "amd64", "/tmp/opf-7")

	deps, err := r.ListDependencies(context.Background(), "vim", false)
	if err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	if len(exec.calls) != 3 {
		t.Fatalf("esperava 3 chamadas (mkdir, update, depends), obteve %d", len(exec.calls))
	}
	if !strings.Contains(strings.Join(exec.calls[1], " "), "update") {
		t.Errorf("segunda chamada deveria ser apt-get update: %v", exec.calls[1])
	}
	if !strings.Contains(strings.Join(exec.calls[1], " "), "Dir::State::lists=/tmp/opf-7/lists") {
		t.Errorf("update sem lists isolado: %v", exec.calls[1])
	}
	cmd := exec.calls[len(exec.calls)-1]
	joined := strings.Join(cmd, " ")
	for _, flag := range []string{"--no-recommends", "--no-suggests", "--no-enhances", "--no-conflicts", "--no-breaks", "--no-replaces"} {
		if !strings.Contains(joined, flag) {
			t.Errorf("comando sem %s: %s", flag, joined)
		}
	}
	if cmd[len(cmd)-1] != "vim" {
		t.Errorf("pacote solicitado deve ser o último argumento: %v", cmd)
	}

	if len(deps) < 50 {
		t.Errorf("lista de dependências curta demais: %d", len(deps))
	}
	if !sort.StringsAreSorted(deps) {
		t.Errorf("dependências não ordenadas: %v", deps)
	}
	for _, want := range []string{"vim", "vim-common", "vim-runtime", "libc6", "libpython3.10", "perl", "debconf"} {
		if !contains(deps, want) {
			t.Errorf("dependência %q ausente em %v", want, deps)
		}
	}
	for _, unwanted := range []string{"<debconf-2.0>", "<perl>", "Depends:"} {
		if contains(deps, unwanted) {
			t.Errorf("entrada inválida %q presente na lista", unwanted)
		}
	}
	seen := make(map[string]int)
	for _, dep := range deps {
		seen[dep]++
	}
	for dep, count := range seen {
		if count != 1 {
			t.Errorf("dependência %q duplicada %d vezes", dep, count)
		}
	}
}

func TestAptResolverIncludeOptionalFlags(t *testing.T) {
	tests := []struct {
		name            string
		includeOptional bool
		present         []string
		absent          []string
	}{
		{
			name:            "sem opcionais",
			includeOptional: false,
			present:         []string{"--no-recommends", "--no-suggests", "--no-enhances"},
		},
		{
			name:            "com opcionais",
			includeOptional: true,
			absent:          []string{"--no-recommends", "--no-suggests", "--no-enhances"},
			present:         []string{"--no-conflicts", "--no-breaks", "--no-replaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExec{output: loadVimFixture(t)}
			r := NewAptResolver(exec, "amd64", "/tmp/opf-7")
			if _, err := r.ListDependencies(context.Background(), "vim", tt.includeOptional); err != nil {
				t.Fatalf("ListDependencies: %v", err)
			}
			joined := strings.Join(exec.calls[len(exec.calls)-1], " ")
			for _, flag := range tt.present {
				if !strings.Contains(joined, flag) {
					t.Errorf("comando sem %q: %s", flag, joined)
				}
			}
			for _, flag := range tt.absent {
				if strings.Contains(joined, flag) {
					t.Errorf("comando com %q inesperado: %s", flag, joined)
				}
			}
		})
	}
}

func TestAptResolverRejectsInvalidPackage(t *testing.T) {
	invalid := []string{"", "Vim", "vim;rm -rf /", "../vim", "vim:", "vim amd64"}
	for _, pkg := range invalid {
		t.Run(pkg, func(t *testing.T) {
			exec := &fakeExec{output: loadVimFixture(t)}
			r := NewAptResolver(exec, "amd64", "/tmp/opf-7")
			if _, err := r.ListDependencies(context.Background(), pkg, false); err == nil {
				t.Fatalf("esperava erro para pacote %q", pkg)
			}
			if len(exec.calls) != 0 {
				t.Fatalf("executor não deveria ser chamado para %q", pkg)
			}
		})
	}
}

func TestAptResolverPropagatesExecError(t *testing.T) {
	exec := &fakeExec{err: errors.New("falha simulada")}
	r := NewAptResolver(exec, "amd64", "/tmp/opf-7")
	_, err := r.ListDependencies(context.Background(), "vim", false)
	if err == nil || !strings.Contains(err.Error(), "falha simulada") {
		t.Fatalf("erro não propagado: %v", err)
	}
}

func TestAptResolverDownloadCommands(t *testing.T) {
	r := NewAptResolver(&fakeExec{}, "amd64", "/tmp/opf-7")
	commands := r.DownloadCommands([]string{"vim", "libc6"})
	if len(commands) != 2 {
		t.Fatalf("esperava 2 comandos, obteve %d", len(commands))
	}

	mkdir := strings.Join(commands[0], " ")
	if !strings.Contains(mkdir, "/tmp/opf-7/lists/partial") || !strings.Contains(mkdir, "/tmp/opf-7/archives/partial") {
		t.Errorf("mkdir sem diretórios isolados: %s", mkdir)
	}

	download := strings.Join(commands[1], " ")
	for _, want := range []string{
		"apt-get",
		"download",
		"Dir::State::lists=/tmp/opf-7/lists",
		"Dir::Cache::archives=/tmp/opf-7/archives",
		"Acquire::Retries=3",
		"APT::Architecture=amd64",
		"vim",
		"libc6",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("download sem %q: %s", want, download)
		}
	}
	if strings.Contains(download, "sh -c") {
		t.Errorf("download não deve depender de shell: %s", download)
	}
}

func TestParseAptCacheDependsAlternativesAndVirtual(t *testing.T) {
	output := "vim\n" +
		"  Depends: <perl>\n" +
		"    perl\n" +
		" |Depends: vim-common\n" +
		"  Depends: libc6\r\n" +
		"<virtual-pkg>\n" +
		"libc6\n" +
		"vim-common\n"

	deps, err := parseAptCacheDepends(output)
	if err != nil {
		t.Fatalf("parseAptCacheDepends: %v", err)
	}
	want := []string{"libc6", "vim", "vim-common"}
	if len(deps) != len(want) {
		t.Fatalf("deps = %v, esperava %v", deps, want)
	}
	for i := range want {
		if deps[i] != want[i] {
			t.Fatalf("deps = %v, esperava %v", deps, want)
		}
	}
}

func TestParseAptCacheDependsEmpty(t *testing.T) {
	if _, err := parseAptCacheDepends(""); err == nil {
		t.Fatal("esperava erro para saída vazia")
	}
}
