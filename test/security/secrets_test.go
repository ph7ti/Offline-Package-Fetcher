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
