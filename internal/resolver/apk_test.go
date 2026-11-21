package resolver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func apkSimulateOutput() string {
	return "Downloading libncursesw-6.4_p20240420-r2\n" +
		"Downloading musl-1.2.5-r4\n" +
		"Downloading vim-9.1.0707-r0\n" +
		"Downloading vim-common-9.1.0707-r0\n" +
		"Downloading xxd-9.1.0707-r0\n"
}

func TestApkResolverListDependencies(t *testing.T) {
	exec := &scriptedExec{fn: func(cmd []string) (string, error) {
		if strings.Contains(strings.Join(cmd, " "), "fetch") && strings.Contains(strings.Join(cmd, " "), "simulate") {
			return apkSimulateOutput(), nil
		}
		return "", nil
	}}
	r := NewApkResolver(exec, "x86_64", "/tmp/opf-7")
	deps, err := r.ListDependencies(context.Background(), "vim", false)
	if err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	want := []string{"libncursesw", "musl", "vim", "vim-common", "xxd"}
	if len(deps) != len(want) {
		t.Fatalf("deps = %v, esperava %v", deps, want)
	}
	for i := range want {
		if deps[i] != want[i] {
			t.Fatalf("deps = %v, esperava %v", deps, want)
		}
	}
	if len(exec.calls) != 3 {
		t.Fatalf("chamadas = %d", len(exec.calls))
	}
	simulate := strings.Join(exec.calls[2], " ")
	for _, flag := range []string{"apk", "fetch", "--recursive", "--simulate", "--arch=x86_64"} {
		if !strings.Contains(simulate, flag) {
			t.Errorf("comando sem %q: %s", flag, simulate)
		}
	}
	if !strings.Contains(strings.Join(exec.calls[0], " "), "/tmp/opf-7/apk-cache") {
		t.Errorf("cache isolado ausente: %v", exec.calls[0])
	}
}

func TestApkResolverDownloadCommands(t *testing.T) {
	r := NewApkResolver(&scriptedExec{fn: func([]string) (string, error) { return "", nil }}, "x86_64", "/tmp/opf-7")
	commands := r.DownloadCommands([]string{"vim", "vim-common"})
	if len(commands) != 2 {
		t.Fatalf("comandos = %d", len(commands))
	}
	download := strings.Join(commands[1], " ")
	for _, want := range []string{
		"apk",
		"--cache-dir", "/tmp/opf-7/apk-cache",
		"fetch", "--recursive", "--arch=x86_64", "--output=/tmp/opf-7",
		"vim vim-common",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("download sem %q: %s", want, download)
		}
	}
}

func TestApkResolverInvalidPackage(t *testing.T) {
	for _, pkg := range []string{"", "vim enhanced", "vim;rm", "../vim", "vim:any"} {
		t.Run(pkg, func(t *testing.T) {
			exec := &scriptedExec{fn: func([]string) (string, error) { return "", nil }}
			r := NewApkResolver(exec, "x86_64", "/tmp/opf-7")
			if _, err := r.ListDependencies(context.Background(), pkg, false); err == nil {
				t.Fatalf("esperava erro para %q", pkg)
			}
			if len(exec.calls) != 0 {
				t.Fatalf("executor não deveria ser chamado para %q", pkg)
			}
		})
	}
}

func TestApkResolverErrors(t *testing.T) {
	exec := &scriptedExec{fn: func(cmd []string) (string, error) {
		return "", errors.New("falha simulada")
	}}
	r := NewApkResolver(exec, "x86_64", "/tmp/opf-7")
	if _, err := r.ListDependencies(context.Background(), "vim", false); err == nil || !strings.Contains(err.Error(), "falha simulada") {
		t.Fatalf("erro não propagado: %v", err)
	}

	emptyExec := &scriptedExec{fn: func(cmd []string) (string, error) {
		if strings.Contains(strings.Join(cmd, " "), "simulate") {
			return "", nil
		}
		return "", nil
	}}
	r2 := NewApkResolver(emptyExec, "x86_64", "/tmp/opf-7")
	if _, err := r2.ListDependencies(context.Background(), "vim", false); err == nil {
		t.Fatal("esperava erro para simulate vazio")
	}
}

func TestApkNameFromNVR(t *testing.T) {
	tests := map[string]string{
		"vim-9.1.0707-r0":                        "vim",
		"vim-common-9.1.0707-r0":                 "vim-common",
		"musl-1.2.5-r4":                          "musl",
		"ncurses-terminfo-base-6.4_p20240420-r2": "ncurses-terminfo-base",
	}
	for nvr, want := range tests {
		got, err := apkNameFromNVR(nvr)
		if err != nil {
			t.Errorf("apkNameFromNVR(%q): %v", nvr, err)
			continue
		}
		if got != want {
			t.Errorf("apkNameFromNVR(%q) = %q, esperava %q", nvr, got, want)
		}
	}
	for _, invalid := range []string{"vim", "vim-1.0", "vim-1.0-rx"} {
		if _, err := apkNameFromNVR(invalid); err == nil {
			t.Errorf("esperava erro para %q", invalid)
		}
	}
}

func TestApkResolverAllowUntrusted(t *testing.T) {
	exec := &scriptedExec{fn: func(cmd []string) (string, error) {
		if strings.Contains(strings.Join(cmd, " "), "simulate") {
			return apkSimulateOutput(), nil
		}
		return "", nil
	}}
	r := NewApkResolver(exec, "x86_64", "/tmp/opf-7")
	r.SetAllowUntrusted(true)
	if _, err := r.ListDependencies(context.Background(), "vim", false); err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	simulate := strings.Join(exec.calls[2], " ")
	if !strings.Contains(simulate, "--allow-untrusted") {
		t.Fatalf("simulação sem --allow-untrusted: %s", simulate)
	}
	commands := r.DownloadCommands([]string{"vim"})
	if len(commands) != 2 || !strings.Contains(strings.Join(commands[1], " "), "--allow-untrusted") {
		t.Fatalf("download sem --allow-untrusted: %v", commands)
	}
}
