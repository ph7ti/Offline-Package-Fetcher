package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/core"
)

// TestSecurityConfigJSONExcludesSecrets asserts sessions.config_json (the only
// session configuration persisted to SQLite and exported in reports) never
// contains SSH, jump-host or sudo passwords.
func TestSecurityConfigJSONExcludesSecrets(t *testing.T) {
	const (
		sshPassword  = "dummy-ssh-password"
		jumpPassword = "dummy-jump-password"
		sudoPassword = "dummy-sudo-password"
	)
	cfg := Config{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		Packages:      []string{"vim", "curl"},
		FetchMethod:   "docker",
		Image:         "ubuntu:22.04",
		RemoteDir:     "/tmp/opf-sessao-1",
		Install:       true,
		UseSudo:       true,
		SudoPassword:  sudoPassword,
		SSH: core.SSHConfig{
			Host: "destino.example", Port: 2222, User: "opf",
			Password: sshPassword, KeyPath: "/home/opf/.ssh/id_ed25519",
			KnownHosts: "/home/opf/.ssh/known_hosts",
			JumpHosts: []core.SSHConfig{{
				Host: "bridge.example", Port: 22, User: "bridge",
				Password: jumpPassword, KeyPath: "/home/bridge/.ssh/id_ed25519",
			}},
		},
		Repositories: []core.PackageRepository{{
			Name: "Interno", Family: "apt", URL: "https://repo.example/apt",
			KeyFile: "/tmp/repo-key.asc", Suite: "bookworm",
		}},
		SyslogEnabled: true,
		SyslogHost:    "127.0.0.1",
		SyslogPort:    514,
		SyslogTag:     "opf",
	}
	service := &Service{cfg: cfg}
	encoded := service.configJSON()

	var payload map[string]any
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("config JSON is not valid JSON: %v (%s)", err, encoded)
	}
	for _, secret := range []string{sshPassword, jumpPassword, sudoPassword} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("secret %q persisted in config JSON: %s", secret, encoded)
		}
	}
	for key, value := range payload {
		lowerKey := strings.ToLower(key)
		for _, marker := range []string{"password", "senha", "secret", "passphrase", "token"} {
			if strings.Contains(lowerKey, marker) {
				t.Fatalf("config JSON key %q looks secret: %s", key, encoded)
			}
		}
		if nested, ok := value.(map[string]any); ok {
			for nestedKey := range nested {
				lowerNested := strings.ToLower(nestedKey)
				for _, marker := range []string{"password", "senha", "secret", "passphrase"} {
					if strings.Contains(lowerNested, marker) {
						t.Fatalf("nested config JSON key %q looks secret: %s", nestedKey, encoded)
					}
				}
			}
		}
	}

	// Non-secret operational metadata must still be present for resume/report.
	for _, key := range []string{"target_os", "packages", "ssh_host", "ssh_user", "ssh_jumps", "repositories"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("config JSON missing %q: %s", key, encoded)
		}
	}
	if !strings.Contains(encoded, `"auth_mode":"key+password"`) {
		t.Fatalf("jump metadata lost its auth mode: %s", encoded)
	}

	// The private key path is metadata (not key material); ensure no PEM blob
	// ever reaches the persisted session.
	for _, marker := range []string{"BEGIN OPENSSH PRIVATE KEY", "BEGIN RSA PRIVATE KEY", "BEGIN PGP"} {
		if strings.Contains(encoded, marker) {
			t.Fatalf("key material persisted in config JSON: %s", encoded)
		}
	}
}

// TestSecurityNewSessionConfigJSONExcludesSecrets asserts the same invariant on
// the exact object handed to the pipeline and persisted by CreateSessao.
func TestSecurityNewSessionConfigJSONExcludesSecrets(t *testing.T) {
	const secret = "dummy-secret-value"
	service := &Service{cfg: Config{
		TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64",
		FetchMethod: "docker", Install: true, UseSudo: true,
		SudoPassword: secret,
		SSH:          core.SSHConfig{Host: "dest", Port: 22, User: "opf", Password: secret},
	}}
	session := service.newSession()
	if session.ConfigJSON == "" {
		t.Fatal("session config JSON is empty")
	}
	if strings.Contains(session.ConfigJSON, secret) {
		t.Fatalf("session config JSON contains a secret: %s", session.ConfigJSON)
	}
}

// TestSecurityPipelineHasNoPasswordSurface is a regression guard: the pipeline
// state and event payloads must never gain access to credential fields.
func TestSecurityPipelineHasNoPasswordSurface(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "core", "pipeline.go"))
	if err != nil {
		t.Fatalf("read pipeline.go: %v", err)
	}
	for _, marker := range []string{"Password", "SudoPassword", "passphrase"} {
		if strings.Contains(string(source), marker) {
			t.Fatalf("pipeline.go references credential field %q; review secret handling", marker)
		}
	}
}
