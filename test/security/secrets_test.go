package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opf/internal/app"
	"opf/internal/config"
	"opf/internal/core"
)

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(encoded)
}

func assertNoSecretMarkers(t *testing.T, label, encoded string) {
	t.Helper()
	lower := strings.ToLower(encoded)
	for _, marker := range []string{"password", "passwd", "senha", "secret", "passphrase", "sudo_password", "private_key", "token"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("%s contains secret marker %q: %s", label, marker, encoded)
		}
	}
}

// TestSettingsNeverPersistSecrets asserts the global settings model has no
// credential fields and the file written by Save stays free of secret markers.
func TestSettingsNeverPersistSecrets(t *testing.T) {
	settings := config.Default()
	settings.SyslogHost = "127.0.0.1"
	assertNoSecretMarkers(t, "settings JSON", mustJSON(t, settings))

	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, settings); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	assertNoSecretMarkers(t, "config file", string(data))

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.FetchMethod != settings.FetchMethod || loaded.SyslogHost != "127.0.0.1" {
		t.Fatalf("settings roundtrip mismatch: %+v", loaded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat config: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config.Save wrote mode %o, want 0600", perm)
		}
	}
}

// TestConfigLoadAcceptsLoosePermissions documents that reading an existing
// config file does not fail when the operator has relaxed its mode: Load only
// parses and validates content. Tightening the mode is Save's responsibility,
// covered by TestSettingsNeverPersistSecrets. This test is platform-neutral
// because Windows does not expose POSIX permission bits.
func TestConfigLoadAcceptsLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil && runtime.GOOS != "windows" {
		t.Fatalf("chmod: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load with 0644 file: %v", err)
	}
	if loaded.FetchMethod != "docker" {
		t.Fatalf("FetchMethod = %q", loaded.FetchMethod)
	}
}

// TestSSHJumpMetadataAndFingerprintExcludePasswords asserts password material
// is not part of persisted metadata nor of the detection fingerprint.
func TestSSHJumpMetadataAndFingerprintExcludePasswords(t *testing.T) {
	const password = "dummy-jump-password"
	inputs := []app.SSHJumpInput{{
		Host: "bridge", Port: "2200", User: "bridge",
		AuthMode: app.SSHJumpAuthPassword, Password: password,
		KnownHosts: "/tmp/known_hosts",
	}}
	metadata := app.SSHJumpMetadataFromInputs(inputs)
	encodedMetadata := mustJSON(t, metadata)
	if strings.Contains(encodedMetadata, password) {
		t.Fatalf("password persisted in metadata: %s", encodedMetadata)
	}
	if !strings.Contains(encodedMetadata, `"auth_mode":"password"`) {
		t.Fatalf("metadata missing auth mode: %s", encodedMetadata)
	}

	other := inputs[0]
	other.Password = "another-dummy-password"
	if app.SSHJumpFingerprint(inputs) != app.SSHJumpFingerprint([]app.SSHJumpInput{other}) {
		t.Fatal("fingerprint changed when only the password changed (password is part of the fingerprint)")
	}
	if strings.Contains(app.SSHJumpFingerprint(inputs), password) {
		t.Fatal("fingerprint contains the password")
	}

	// Changing a non-secret property must change the fingerprint, otherwise a
	// stale detection could be reused against a different host.
	other = inputs[0]
	other.Host = "other-bridge"
	if app.SSHJumpFingerprint(inputs) == app.SSHJumpFingerprint([]app.SSHJumpInput{other}) {
		t.Fatal("fingerprint ignored the host change")
	}
}

// TestProfilesNeverPersistSecrets asserts saved session profiles strip all
// ephemeral repository credentials and never contain secret markers on disk.
func TestProfilesNeverPersistSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	profile := config.Profile{
		Name:          "prod-ubuntu",
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "podman",
		SSHHost:       "10.0.0.10",
		SSHPort:       22,
		SSHUser:       "ops",
		SSHUseAgent:   true,
		Repositories: []core.PackageRepository{{
			Name:         "corp",
			Family:       "apt",
			URL:          "https://repo.corp.local/apt",
			Suite:        "jammy",
			KeyFile:      "/tmp/key.asc",
			AuthType:     core.RepositoryAuthBasic,
			AuthUsername: "ci-user",
			AuthPassword: "super-secret-repo-password",
			AuthToken:    "super-secret-bearer-token",
		}},
	}
	if err := config.UpsertProfile(path, profile); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	raw := string(data)
	assertNoSecretMarkers(t, "profiles file", raw)
	for _, secret := range []string{"super-secret-repo-password", "super-secret-bearer-token", "ci-user"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("profiles file leaked ephemeral repo auth %q: %s", secret, raw)
		}
	}
}

// TestPrivateRepoAuthNeverSerialized asserts PackageRepository JSON marshaling
// omits all ephemeral authentication fields via json:"-".
func TestPrivateRepoAuthNeverSerialized(t *testing.T) {
	repo := core.PackageRepository{
		Name:         "private-rpm",
		Family:       "rpm",
		URL:          "https://repo.corp.local/rpm",
		KeyFile:      "/tmp/rpm.key",
		AuthType:     core.RepositoryAuthBearer,
		AuthUsername: "ignored-user",
		AuthPassword: "ignored-password",
		AuthToken:    "ephemeral-bearer-xyz",
	}
	encoded := mustJSON(t, repo)
	assertNoSecretMarkers(t, "PackageRepository JSON", encoded)
	if strings.Contains(encoded, "ephemeral-bearer-xyz") || strings.Contains(encoded, "ignored-user") {
		t.Fatalf("ephemeral auth serialized: %s", encoded)
	}
}

// TestProxySettingsRejectEmbeddedCredentials asserts config.Save rejects proxy
// URLs carrying embedded userinfo so proxy credentials are never written to disk.
func TestProxySettingsRejectEmbeddedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings := config.Default()
	settings.HTTPSProxy = "http://alice:s3cr3t@proxy.corp.local:8080"
	if err := config.Save(path, settings); err == nil {
		t.Fatal("config.Save accepted proxy URL with embedded credentials")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("config.Save wrote file despite rejecting proxy credentials")
	}
}
