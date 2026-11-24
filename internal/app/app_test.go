package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"opf/internal/core"
)

func baseConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		StorePath:     filepath.Join(t.TempDir(), "opf.db"),
		OutputBaseDir: t.TempDir(),
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		Packages:      []string{"vim"},
		FetchMethod:   "docker",
	}
}

func TestNewValidations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"sem alvo", func(c *Config) { c.TargetOS = "" }},
		{"sem versão", func(c *Config) { c.TargetVersion = "" }},
		{"sem arch", func(c *Config) { c.TargetArch = "" }},
		{"distro inválida", func(c *Config) { c.TargetOS = "archlinux" }},
		{"método inválido", func(c *Config) { c.FetchMethod = "lxc" }},
		{"wsl não-apt", func(c *Config) { c.TargetOS = "centos"; c.TargetVersion = "7"; c.FetchMethod = "wsl" }},
		{"install sem SSH", func(c *Config) {
			c.Install = true
			c.ConfirmInstall = func(context.Context, []core.PackageArtifact) (bool, error) { return true, nil }
		}},
		{"install sem confirmação", func(c *Config) { c.Install = true; c.SSH.Host = "host" }},
		{"sudo sem senha", func(c *Config) {
			c.Install = true
			c.SSH.Host = "host"
			c.UseSudo = true
			c.ConfirmInstall = func(context.Context, []core.PackageArtifact) (bool, error) { return true, nil }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig(t)
			tt.mutate(&cfg)
			service, err := New(cfg)
			if err == nil {
				service.Close()
				t.Fatal("esperava erro de configuração")
			}
		})
	}
}

func TestNewDockerLazyService(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Packages = []string{"vim", "curl", "vim"}
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer service.Close()
	if len(service.Settings().Packages) != 2 {
		t.Fatalf("pacotes não deduplicados: %v", service.Settings().Packages)
	}
	if service.fetcher == nil {
		t.Fatal("fetcher não configurado")
	}
	if service.transfer != nil || service.inspect != nil {
		t.Fatal("transferência não deveria existir sem SSH")
	}
}

func TestNewWithSyslog(t *testing.T) {
	cfg := baseConfig(t)
	cfg.SyslogEnabled = true
	cfg.SyslogHost = "127.0.0.1"
	cfg.SyslogPort = 514
	cfg.SyslogProto = "udp"
	cfg.SyslogTag = "opftest"
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if service.eventSink == nil {
		t.Fatal("event sink não configurado")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewPodmanLazyService(t *testing.T) {
	cfg := baseConfig(t)
	cfg.FetchMethod = "podman"
	cfg.ContainerHost = "unix:///run/user/1000/podman/podman.sock"
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New(podman): %v", err)
	}
	defer service.Close()
	if service.fetcher == nil {
		t.Fatal("fetcher não configurado para podman")
	}
	if service.Settings().FetchMethod != "podman" {
		t.Fatalf("FetchMethod = %q, want podman", service.Settings().FetchMethod)
	}
}

func TestResolveFetchMethod(t *testing.T) {
	if got := resolveFetchMethod("docker", "apt"); got != "docker" {
		t.Fatalf("docker = %q", got)
	}
	if got := resolveFetchMethod("podman", "rpm"); got != "podman" {
		t.Fatalf("podman = %q", got)
	}
	if got := resolveFetchMethod("auto", "rpm"); got != "docker" {
		t.Fatalf("auto rpm = %q", got)
	}
	if runtime.GOOS == "windows" {
		if got := resolveFetchMethod("auto", "apt"); got != "wsl" {
			t.Fatalf("auto windows apt = %q", got)
		}
	} else if got := resolveFetchMethod("auto", "apt"); got != "docker" {
		t.Fatalf("auto não-windows = %q", got)
	}
}
