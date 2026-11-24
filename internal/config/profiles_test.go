package config

import (
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/core"
)

func TestProfilesCRUDAndSanitization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")

	// Empty initially.
	list, err := LoadProfiles(path)
	if err != nil || len(list) != 0 {
		t.Fatalf("LoadProfiles inicial = %v, %v", list, err)
	}

	p := Profile{
		Name:          "prod-dmz-ubuntu22",
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "podman",
		SSHHost:       "10.20.0.15",
		SSHPort:       22,
		SSHUser:       "opf",
		SSHUseAgent:   true,
		Packages:      []string{"nginx=1.18.0-6ubuntu14.4"},
		Repositories: []core.PackageRepository{{
			Name:         "Interno",
			Family:       "apt",
			URL:          "https://repo.corp/apt",
			KeyFile:      "/etc/keys/repo.gpg",
			Suite:        "jammy",
			AuthType:     core.RepositoryAuthBasic,
			AuthUsername: "bot",
			AuthPassword: "super-secret-repo-password",
		}},
	}
	if err := UpsertProfile(path, p); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}

	got, err := GetProfile(path, "PROD-DMZ-UBUNTU22")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.TargetOS != "ubuntu" || got.FetchMethod != "podman" || !got.SSHUseAgent {
		t.Fatalf("perfil carregado inesperado: %+v", got)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].AuthPassword != "" || got.Repositories[0].AuthType != "" {
		t.Fatalf("credenciais de repositório não deveriam ser persistidas: %+v", got.Repositories)
	}

	if err := DeleteProfile(path, "prod-dmz-ubuntu22"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, err := GetProfile(path, "prod-dmz-ubuntu22"); err == nil || !strings.Contains(err.Error(), "não encontrado") {
		t.Fatalf("esperava perfil removido, obteve %v", err)
	}
}
