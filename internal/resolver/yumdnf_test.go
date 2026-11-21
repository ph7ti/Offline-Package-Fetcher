package resolver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type scriptedExec struct {
	fn    func(cmd []string) (string, error)
	calls [][]string
}

func (e *scriptedExec) Exec(_ context.Context, cmd []string) (string, error) {
	e.calls = append(e.calls, append([]string(nil), cmd...))
	return e.fn(cmd)
}

func yumRepoqueryOutput() string {
	return "Loaded plugins: fastestmirror\n" +
		"libacl\n" +
		"glibc\n" +
		"glibc\n" +
		"vim-common\n" +
		"rpmlib(CompressedFileNames) <= 3.0.4-1\n"
}

func TestYumDnfResolverListWithYum(t *testing.T) {
	exec := &scriptedExec{fn: func(cmd []string) (string, error) {
		if cmd[0] == "sh" {
			return "yum\n", nil
		}
		return yumRepoqueryOutput(), nil
	}}
	r := NewYumDnfResolver(exec, "x86_64", "/tmp/opf-7")
	deps, err := r.ListDependencies(context.Background(), "vim-enhanced", false)
	if err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	want := []string{"glibc", "libacl", "vim-common", "vim-enhanced"}
	if len(deps) != len(want) {
		t.Fatalf("deps = %v, esperava %v", deps, want)
	}
	for i := range want {
		if deps[i] != want[i] {
			t.Fatalf("deps = %v, esperava %v", deps, want)
		}
	}
	if len(exec.calls) != 2 {
		t.Fatalf("chamadas = %d", len(exec.calls))
	}
	listCmd := strings.Join(exec.calls[1], " ")
	for _, flag := range []string{"repoquery", "--requires", "--resolve", "--recursive", "--queryformat=%{name}"} {
		if !strings.Contains(listCmd, flag) {
			t.Errorf("repoquery sem %q: %s", flag, listCmd)
		}
	}
	if !strings.HasSuffix(listCmd, "vim-enhanced") {
		t.Errorf("pacote não é o último argumento: %s", listCmd)
	}
}

func TestYumDnfResolverDownloadCommands(t *testing.T) {
	tests := []struct {
		name            string
		manager         string
		includeOptional bool
		wantManager     []string
		absent          []string
		wantSuffix      string
	}{
		{
			name:            "yum",
			manager:         "yum",
			includeOptional: false,
			wantManager:     []string{"yumdownloader", "--resolve", "--archlist=x86_64", "--destdir=/tmp/opf-7"},
			wantSuffix:      "vim-enhanced glibc",
		},
		{
			name:            "dnf sem opcionais",
			manager:         "dnf",
			includeOptional: false,
			wantManager:     []string{"dnf", "--setopt=install_weak_deps=False", "download", "--resolve", "--alldeps", "--arch=x86_64", "--destdir=/tmp/opf-7"},
			wantSuffix:      "vim-enhanced",
		},
		{
			name:            "dnf com opcionais",
			manager:         "dnf",
			includeOptional: true,
			wantManager:     []string{"dnf", "download", "--resolve", "--alldeps", "--arch=x86_64", "--destdir=/tmp/opf-7"},
			wantSuffix:      "vim-enhanced",
			absent:          []string{"install_weak_deps"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &scriptedExec{fn: func(cmd []string) (string, error) {
				if cmd[0] == "sh" {
					return tt.manager + "\n", nil
				}
				return "glibc\n", nil
			}}
			r := NewYumDnfResolver(exec, "x86_64", "/tmp/opf-7")
			if _, err := r.ListDependencies(context.Background(), "vim-enhanced", tt.includeOptional); err != nil {
				t.Fatalf("ListDependencies: %v", err)
			}
			commands := r.DownloadCommands([]string{"vim-enhanced", "glibc"})
			if len(commands) != 2 {
				t.Fatalf("comandos = %d", len(commands))
			}
			download := strings.Join(commands[1], " ")
			for _, want := range tt.wantManager {
				if !strings.Contains(download, want) {
					t.Errorf("download sem %q: %s", want, download)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(download, absent) {
					t.Errorf("download com %q inesperado: %s", absent, download)
				}
			}
			if !strings.HasSuffix(download, tt.wantSuffix) {
				t.Errorf("pacotes não foram passados (esperava sufixo %q): %s", tt.wantSuffix, download)
			}
		})
	}
}

func TestYumDnfResolverRejectsInvalidPackage(t *testing.T) {
	for _, pkg := range []string{"", "vim enhanced", "vim;rm", "../vim", "vim:amd64"} {
		t.Run(pkg, func(t *testing.T) {
			exec := &scriptedExec{fn: func([]string) (string, error) { return "vim\n", nil }}
			r := NewYumDnfResolver(exec, "x86_64", "/tmp/opf-7")
			if _, err := r.ListDependencies(context.Background(), pkg, false); err == nil {
				t.Fatalf("esperava erro para %q", pkg)
			}
			if len(exec.calls) != 0 {
				t.Fatalf("executor não deveria ser chamado para %q", pkg)
			}
		})
	}
}

func TestYumDnfResolverErrors(t *testing.T) {
	exec := &scriptedExec{fn: func(cmd []string) (string, error) {
		return "", errors.New("falha simulada")
	}}
	r := NewYumDnfResolver(exec, "x86_64", "/tmp/opf-7")
	if _, err := r.ListDependencies(context.Background(), "vim-enhanced", false); err == nil || !strings.Contains(err.Error(), "falha simulada") {
		t.Fatalf("erro não propagado: %v", err)
	}

	emptyExec := &scriptedExec{fn: func(cmd []string) (string, error) {
		if cmd[0] == "sh" {
			return "yum\n", nil
		}
		return "", nil
	}}
	r2 := NewYumDnfResolver(emptyExec, "x86_64", "/tmp/opf-7")
	if _, err := r2.ListDependencies(context.Background(), "vim-enhanced", false); err == nil {
		t.Fatal("esperava erro para repoquery vazio")
	}

	unknownExec := &scriptedExec{fn: func([]string) (string, error) { return "zypper\n", nil }}
	r3 := NewYumDnfResolver(unknownExec, "x86_64", "/tmp/opf-7")
	if _, err := r3.ListDependencies(context.Background(), "vim-enhanced", false); err == nil {
		t.Fatal("esperava erro para gerenciador desconhecido")
	}
}

func TestParseRepoquerySkipsWarnings(t *testing.T) {
	deps, err := parseRepoquery("Last metadata expiration check: 0:00:01 ago\nbash\nbash\nrpmlib(Foo) <= 1\n")
	if err != nil {
		t.Fatalf("parseRepoquery: %v", err)
	}
	if len(deps) != 1 || deps[0] != "bash" {
		t.Fatalf("deps = %v", deps)
	}
}
