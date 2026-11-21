package inspector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opf/internal/core"
)

type fakeTM struct {
	output  string
	err     error
	command string
}

func (f *fakeTM) Connect(context.Context, core.SSHConfig) error         { return nil }
func (f *fakeTM) Upload(context.Context, []string, string) error        { return nil }
func (f *fakeTM) VerifyChecksums(context.Context, string, string) error { return nil }
func (f *fakeTM) RunCommand(_ context.Context, cmd string) (string, error) {
	f.command = cmd
	return f.output, f.err
}
func (f *fakeTM) Close() error { return nil }

type hostTM struct{ fn func(string) string }

func (h *hostTM) Connect(context.Context, core.SSHConfig) error            { return nil }
func (h *hostTM) Upload(context.Context, []string, string) error           { return nil }
func (h *hostTM) VerifyChecksums(context.Context, string, string) error    { return nil }
func (h *hostTM) RunCommand(_ context.Context, cmd string) (string, error) { return h.fn(cmd), nil }
func (h *hostTM) Close() error                                             { return nil }

func TestInspectorCollectHostInfo(t *testing.T) {
	tm := &hostTM{fn: func(cmd string) string {
		switch {
		case cmd == "id -un":
			return "opf"
		case cmd == "uname -r":
			return "6.8.0-test"
		case cmd == "cat /proc/uptime":
			return "90061.5 0.0"
		case cmd == "nproc":
			return "4"
		case strings.Contains(cmd, "model name"):
			return "Example CPU"
		case strings.Contains(cmd, "MemTotal"):
			return "8192 MB total, 4096 MB disponível"
		case strings.Contains(cmd, "df -h"):
			return "100G total, 50G livre"
		default:
			return ""
		}
	}}
	inspector, _ := New("apt", tm)
	info, err := inspector.CollectHostInfo(context.Background())
	if err != nil {
		t.Fatalf("CollectHostInfo: %v", err)
	}
	if info.Usuario != "opf" || info.Kernel != "6.8.0-test" || info.Uptime != "1d 1h 1m" || !strings.Contains(info.CPU, "4 cores") || !strings.Contains(info.Memoria, "8192") || !strings.Contains(info.Disco, "100G") {
		t.Fatalf("host info = %+v", info)
	}
}

func TestInspectorAPT(t *testing.T) {
	tm := &fakeTM{output: "ii \tvim\t2:8.2\tamd64\nun \tlibfoo\t1.0\tamd64\n"}
	inspector, err := New("apt", tm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := inspector.InspectPackages(context.Background(), []string{"vim", "libfoo"})
	if err != nil {
		t.Fatalf("InspectPackages: %v", err)
	}
	if len(got) != 1 || got[0].Name != "vim" || got[0].Version != "2:8.2" {
		t.Fatalf("instalados = %+v", got)
	}
	if !strings.Contains(tm.command, "dpkg-query") || !strings.Contains(tm.command, "'vim'") {
		t.Fatalf("comando APT inesperado: %s", tm.command)
	}
}

func TestInspectorRPM(t *testing.T) {
	tm := &fakeTM{output: "vim-enhanced\t7.4.629-8.el7_9\tx86_64\n"}
	inspector, err := New("rpm", tm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := inspector.InspectPackages(context.Background(), []string{"vim-enhanced"})
	if err != nil {
		t.Fatalf("InspectPackages: %v", err)
	}
	if len(got) != 1 || got[0].Name != "vim-enhanced" || got[0].Arch != "x86_64" {
		t.Fatalf("instalados = %+v", got)
	}
	if !strings.Contains(tm.command, "rpm -q") {
		t.Fatalf("comando RPM inesperado: %s", tm.command)
	}
}

func TestInspectorAPK(t *testing.T) {
	tm := &fakeTM{output: "vim-9.1.0707-r0\n"}
	inspector, err := New("apk", tm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := inspector.InspectPackages(context.Background(), []string{"vim"})
	if err != nil {
		t.Fatalf("InspectPackages: %v", err)
	}
	if len(got) != 1 || got[0].Name != "vim" || got[0].Version != "9.1.0707-r0" {
		t.Fatalf("instalados = %+v", got)
	}
	if !strings.Contains(tm.command, "apk info -e") {
		t.Fatalf("comando APK inesperado: %s", tm.command)
	}
}

func TestInspectorErrorsAndValidation(t *testing.T) {
	if _, err := New("zypper", &fakeTM{}); err == nil {
		t.Fatal("esperava erro para família inválida")
	}
	if _, err := New("apt", nil); err == nil {
		t.Fatal("esperava erro sem transferência")
	}
	inspector, _ := New("apt", &fakeTM{})
	if _, err := inspector.InspectPackages(context.Background(), []string{"vim;rm"}); err == nil {
		t.Fatal("esperava erro para nome inválido")
	}
	if _, err := inspector.InspectPackages(context.Background(), nil); err == nil {
		t.Fatal("esperava erro sem pacotes")
	}
	tm := &fakeTM{err: errors.New("falha remota")}
	inspector2, _ := New("apt", tm)
	if _, err := inspector2.InspectPackages(context.Background(), []string{"vim"}); err == nil || !strings.Contains(err.Error(), "falha remota") {
		t.Fatalf("erro não propagado: %v", err)
	}
}
