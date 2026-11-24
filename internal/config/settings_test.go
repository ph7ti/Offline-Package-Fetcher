package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSaveLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opf", "config.json")
	settings := Default()
	settings.FetchMethod = "auto"
	settings.TempDir = filepath.Join(t.TempDir(), "temp")
	settings.DatabasePath = filepath.Join(t.TempDir(), "opf.db")
	settings.StageTimeoutSecs = 600
	settings.IncludeOptional = true
	settings.DockerImage = "ubuntu:22.04"
	settings.SyslogHost = "syslog.local"
	settings.SyslogPort = 514
	if err := Save(path, settings); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissões = %o", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.FetchMethod != "auto" || !loaded.IncludeOptional || loaded.DockerImage != settings.DockerImage || loaded.DatabasePath != settings.DatabasePath {
		t.Fatalf("configurações divergentes: %+v", loaded)
	}
	if loaded.StageTimeout() != 10*time.Minute {
		t.Fatalf("timeout = %v", loaded.StageTimeout())
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	settings, err := Load(filepath.Join(t.TempDir(), "nao-existe.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.FetchMethod != "docker" || settings.StageTimeout() != 10*time.Minute {
		t.Fatalf("default inesperado: %+v", settings)
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := (Settings{FetchMethod: "podman"}).Validate(); err != nil {
		t.Fatalf("podman deveria ser aceito: %v", err)
	}
	tests := []Settings{
		{FetchMethod: "lxc"},
		{HTTPProxy: "http://user:pass@proxy.corp:8080"},
		{StageTimeoutSecs: -1},
		{SyslogPort: 70000},
		{SyslogProto: "sctp"},
	}
	for _, settings := range tests {
		if err := settings.Validate(); err == nil {
			t.Fatalf("esperava erro para %+v", settings)
		}
	}
}
