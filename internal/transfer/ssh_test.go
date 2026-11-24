package transfer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func writePrivateKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("criar signer: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("serializar chave: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("escrever chave: %v", err)
	}
	return path, signer.PublicKey()
}

func writeKnownHosts(t *testing.T, address string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{address}, key)+"\n"), 0o600); err != nil {
		t.Fatalf("escrever known_hosts: %v", err)
	}
	return path
}

func TestBuildSSHClientConfigHostKey(t *testing.T) {
	keyPath, publicKey := writePrivateKey(t)
	knownHostsPath := writeKnownHosts(t, "[127.0.0.1]:2222", publicKey)

	cfg := SSHConfig{
		Host:       "127.0.0.1",
		Port:       2222,
		User:       "opf",
		KeyPath:    keyPath,
		KnownHosts: knownHostsPath,
	}
	clientConfig, err := buildSSHClientConfig(cfg)
	if err != nil {
		t.Fatalf("buildSSHClientConfig: %v", err)
	}
	if clientConfig.User != "opf" {
		t.Fatalf("usuário = %q", clientConfig.User)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, publicKey); err != nil {
		t.Fatalf("host key conhecida rejeitada: %v", err)
	}

	_, otherPublicKey := writePrivateKey(t)
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, otherPublicKey); err == nil {
		t.Fatal("host key desconhecida deveria ser rejeitada")
	}
}

func TestBuildSSHClientConfigUnknownHostKey(t *testing.T) {
	keyPath, _ := writePrivateKey(t)
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHostsPath, nil, 0o600); err != nil {
		t.Fatalf("criar known_hosts vazio: %v", err)
	}
	cfg := SSHConfig{
		Host:       "127.0.0.1",
		Port:       2222,
		User:       "opf",
		KeyPath:    keyPath,
		KnownHosts: knownHostsPath,
	}
	clientConfig, err := buildSSHClientConfig(cfg)
	if err != nil {
		t.Fatalf("buildSSHClientConfig: %v", err)
	}
	_, unknownKey := writePrivateKey(t)
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", remote, unknownKey); err == nil {
		t.Fatal("known_hosts vazio não pode aceitar host key")
	}
}

func TestBuildSSHClientConfigSkipHostKey(t *testing.T) {
	keyPath, _ := writePrivateKey(t)
	cfg := SSHConfig{
		Host: "127.0.0.1", Port: 2222, User: "opf", KeyPath: keyPath,
		KnownHosts:          filepath.Join(t.TempDir(), "nao-existe"),
		InsecureSkipHostKey: true,
	}
	clientConfig, err := buildSSHClientConfig(cfg)
	if err != nil {
		t.Fatalf("buildSSHClientConfig com skip: %v", err)
	}
	_, unknownKey := writePrivateKey(t)
	if err := clientConfig.HostKeyCallback("127.0.0.1:2222", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, unknownKey); err != nil {
		t.Fatalf("skip deveria aceitar host key: %v", err)
	}
}

func TestResolveKnownHostsMissingFile(t *testing.T) {
	if _, err := resolveKnownHosts(filepath.Join(t.TempDir(), "nao-existe")); err == nil {
		t.Fatal("esperava erro para known_hosts inexistente")
	}
}

func TestResolveKnownHostsDefault(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatalf("criar .ssh: %v", err)
	}
	defaultPath := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.WriteFile(defaultPath, nil, 0o600); err != nil {
		t.Fatalf("criar known_hosts: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	resolved, err := resolveKnownHosts("")
	if err != nil {
		t.Fatalf("resolveKnownHosts: %v", err)
	}
	if resolved != defaultPath {
		t.Fatalf("known_hosts padrão = %q, esperava %q", resolved, defaultPath)
	}
}

func TestValidateSSHConfig(t *testing.T) {
	valid := SSHConfig{Host: "host", Port: 22, User: "opf", KeyPath: "key"}
	if err := validateSSHConfig(valid, "destino"); err != nil {
		t.Fatalf("config válida rejeitada: %v", err)
	}

	tests := []struct {
		name string
		cfg  SSHConfig
	}{
		{"sem host", SSHConfig{User: "opf", KeyPath: "key"}},
		{"sem usuário", SSHConfig{Host: "host", KeyPath: "key"}},
		{"sem auth", SSHConfig{Host: "host", User: "opf"}},
		{"porta inválida", SSHConfig{Host: "host", Port: 70000, User: "opf", KeyPath: "key"}},
		{"host com newline", SSHConfig{Host: "host\nx", User: "opf", KeyPath: "key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSSHConfig(tt.cfg, "destino"); err == nil {
				t.Fatal("esperava erro de validação")
			}
		})
	}
}

func TestSSHTransferConnectRejectsNestedJumps(t *testing.T) {
	keyPath, _ := writePrivateKey(t)
	cfg := SSHConfig{
		Host: "destino", Port: 22, User: "opf", KeyPath: keyPath,
		KnownHosts: filepath.Join(t.TempDir(), "known_hosts"),
		JumpHosts: []SSHConfig{{
			Host: "jump", User: "opf", KeyPath: keyPath,
			JumpHosts: []SSHConfig{{Host: "outro", User: "opf", KeyPath: keyPath}},
		}},
	}
	manager := NewSSHTransferManager()
	err := manager.Connect(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "aninhados") {
		t.Fatalf("esperava erro de jumps aninhados: %v", err)
	}
}

func TestSSHTransferNotConnected(t *testing.T) {
	manager := NewSSHTransferManager()
	if err := manager.Upload(context.Background(), []string{"x"}, "/tmp"); err == nil {
		t.Fatal("Upload deveria falhar sem conexão")
	}
	if err := manager.VerifyChecksums(context.Background(), "/tmp/SHA256SUMS", "/tmp"); err == nil {
		t.Fatal("VerifyChecksums deveria falhar sem conexão")
	}
	if _, err := manager.RunCommand(context.Background(), "true"); err == nil {
		t.Fatal("RunCommand deveria falhar sem conexão")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close sem conexão: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"/tmp/opf":    "'/tmp/opf'",
		"a'b":         `'a'\''b'`,
		"":            "''",
		"a b":         "'a b'",
		"$(rm -rf /)": "'$(rm -rf /)'",
	}
	for input, want := range tests {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %q, esperava %q", input, got, want)
		}
	}
}

func TestValidateRemotePath(t *testing.T) {
	if err := validateRemotePath("/tmp/opf"); err != nil {
		t.Fatalf("caminho válido rejeitado: %v", err)
	}
	for _, invalid := range []string{"", " ", "a\nb", "a\rb", "a\x00b"} {
		if err := validateRemotePath(invalid); err == nil {
			t.Errorf("esperava erro para %q", invalid)
		}
	}
}

func TestCopyWithContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var builder strings.Builder
	if _, err := copyWithContext(ctx, &builder, strings.NewReader("abc")); err == nil {
		t.Fatal("esperava erro de contexto cancelado")
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := expandHome("~/x")
	if err != nil {
		t.Fatalf("expandHome: %v", err)
	}
	if got != filepath.Join(home, "x") {
		t.Fatalf("expandHome = %q", got)
	}
	got, err = expandHome("/absolute")
	if err != nil || got != "/absolute" {
		t.Fatalf("expandHome absoluto = %q, %v", got, err)
	}
}

func TestSSHAuthEncryptedPrivateKeyWithPassphrase(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	const passphrase = "dummy-key-passphrase"
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("serializar chave cifrada: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519_enc")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("escrever chave: %v", err)
	}

	// Missing passphrase gives a clear hint.
	if _, _, err := sshAuthMethods(SSHConfig{Host: "h", User: "u", KeyPath: keyPath}); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("esperava erro orientando passphrase, obteve %v", err)
	}
	// Wrong passphrase fails.
	if _, _, err := sshAuthMethods(SSHConfig{Host: "h", User: "u", KeyPath: keyPath, KeyPassphrase: "wrong"}); err == nil {
		t.Fatal("esperava erro para passphrase incorreta")
	}
	// Valid passphrase succeeds.
	methods, _, err := sshAuthMethods(SSHConfig{Host: "h", User: "u", KeyPath: keyPath, KeyPassphrase: passphrase})
	if err != nil || len(methods) != 1 {
		t.Fatalf("sshAuthMethods com passphrase = %d, %v", len(methods), err)
	}
}

func TestValidateSSHConfigAllowsUseAgent(t *testing.T) {
	if err := validateSSHConfig(SSHConfig{Host: "host", Port: 22, User: "opf", UseAgent: true}, "destino"); err != nil {
		t.Fatalf("UseAgent deveria satisfazer validação de auth: %v", err)
	}
}
