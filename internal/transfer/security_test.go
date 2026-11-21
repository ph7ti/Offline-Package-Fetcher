package transfer

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestSecurityHostKeyPolicyStrictByDefault asserts the default SSH policy is
// strict: an empty known_hosts rejects every host key, a matching entry is
// accepted, a hostname mismatch is rejected and a missing file is fatal.
func TestSecurityHostKeyPolicyStrictByDefault(t *testing.T) {
	keyPath, publicKey := writePrivateKey(t)
	emptyKnownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(emptyKnownHosts, nil, 0o600); err != nil {
		t.Fatalf("create empty known_hosts: %v", err)
	}

	base := SSHConfig{
		Host: "127.0.0.1", Port: 2222, User: "opf",
		KeyPath: keyPath, KnownHosts: emptyKnownHosts,
	}
	clientConfig, err := buildSSHClientConfig(base)
	if err != nil {
		t.Fatalf("buildSSHClientConfig: %v", err)
	}
	if clientConfig.HostKeyCallback == nil {
		t.Fatal("strict policy returned a nil callback")
	}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, publicKey); err == nil {
		t.Fatal("empty known_hosts accepted an unknown host key")
	}

	knownHostsPath := writeKnownHosts(t, "[127.0.0.1]:2222", publicKey)
	strict := base
	strict.KnownHosts = knownHostsPath
	clientConfig, err = buildSSHClientConfig(strict)
	if err != nil {
		t.Fatalf("buildSSHClientConfig with entry: %v", err)
	}
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, publicKey); err != nil {
		t.Fatalf("matching host key rejected: %v", err)
	}
	// Same key, different hostname/port: the known_hosts line must not match.
	if err := clientConfig.HostKeyCallback("127.0.0.1:2223", remote, publicKey); err == nil {
		t.Fatal("host key accepted for a hostname absent from known_hosts")
	}

	// A different key for the same host must be rejected (MITM).
	_, otherKey := writePrivateKey(t)
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, otherKey); err == nil {
		t.Fatal("unknown host key accepted for a known host")
	}

	// Missing known_hosts file is an error, never a silent skip.
	missing := base
	missing.KnownHosts = filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := buildSSHClientConfig(missing); err == nil {
		t.Fatal("missing known_hosts accepted")
	}
}

// TestSecurityInsecureSkipHostKeyIsExplicitOnly asserts the opt-in is the only
// path that disables verification, and it still requires an explicit field.
func TestSecurityInsecureSkipHostKeyIsExplicitOnly(t *testing.T) {
	keyPath, _ := writePrivateKey(t)
	cfg := SSHConfig{
		Host: "127.0.0.1", Port: 2222, User: "opf", KeyPath: keyPath,
		KnownHosts:          filepath.Join(t.TempDir(), "does-not-exist"),
		InsecureSkipHostKey: true,
	}
	clientConfig, err := buildSSHClientConfig(cfg)
	if err != nil {
		t.Fatalf("buildSSHClientConfig: %v", err)
	}
	_, unknownKey := writePrivateKey(t)
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, unknownKey); err != nil {
		t.Fatalf("explicit insecure opt-in rejected: %v", err)
	}
}

// TestSecurityShellQuote asserts the transfer quoting helper used for remote
// dirs, manifests and cleanup paths.
func TestSecurityShellQuote(t *testing.T) {
	tests := map[string]string{
		"/tmp/opf": "'/tmp/opf'",
		"a'b":      `'a'\''b'`,
		"a;id":     "'a;id'",
		"$(id)":    "'$(id)'",
		"`id`":     "'`id`'",
		"a b":      "'a b'",
		"":         "''",
	}
	for input, want := range tests {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestSecurityValidateRemotePath asserts remote paths cannot smuggle control
// characters into SFTP calls or remote commands.
func TestSecurityValidateRemotePath(t *testing.T) {
	for _, valid := range []string{"/tmp/opf", "SHA256SUMS", "a/b.deb", "a b"} {
		if err := validateRemotePath(valid); err != nil {
			t.Errorf("valid path %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", " ", "a\nb", "a\rb", "a\x00b"} {
		if err := validateRemotePath(invalid); err == nil {
			t.Errorf("invalid path %q accepted", invalid)
		}
	}
}

// TestSecuritySSHConfigRejectsControlChars asserts host and credential-policy
// validation rejects control characters before any connection attempt.
func TestSecuritySSHConfigRejectsControlChars(t *testing.T) {
	base := SSHConfig{Host: "host", Port: 22, User: "opf", KeyPath: "/tmp/key"}
	if err := validateSSHConfig(base, "destino"); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, host := range []string{"a\nb", "a\rb", "a\x00b"} {
		cfg := base
		cfg.Host = host
		if err := validateSSHConfig(cfg, "destino"); err == nil {
			t.Errorf("host %q accepted", host)
		}
	}
	if err := validateSSHConfig(SSHConfig{Host: "host", Port: 22, User: "u"}, "destino"); err == nil {
		t.Error("config without key or password accepted")
	}
}
