package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSSHConfigAliasAndProxyJump(t *testing.T) {
	dir := t.TempDir()
	subCfg := filepath.Join(dir, "extra.conf")
	if err := os.WriteFile(subCfg, []byte(`
Host bastion
  HostName 10.0.0.5
  User jumpuser
  Port 2222
  IdentityFile /keys/bastion_ed25519
`), 0o600); err != nil {
		t.Fatal(err)
	}

	mainCfg := filepath.Join(dir, "config")
	content := "Include extra.conf\n" + `
Host prod-db
  HostName 10.20.30.40
  User opf
  Port 2200
  IdentityFile /keys/prod_ed25519
  ProxyJump bastion
  UserKnownHostsFile /keys/known_hosts

Host *
  User fallback
  Port 2299
`
	if err := os.WriteFile(mainCfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, err := ResolveSSHConfigAlias(mainCfg, "prod-db")
	if err != nil {
		t.Fatalf("ResolveSSHConfigAlias: %v", err)
	}
	if entry.Host != "10.20.30.40" || entry.User != "opf" || entry.Port != 2200 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.KeyPath != "/keys/prod_ed25519" || entry.KnownHosts != "/keys/known_hosts" {
		t.Fatalf("paths = %+v", entry)
	}
	if len(entry.JumpHosts) != 1 {
		t.Fatalf("jumps = %+v", entry.JumpHosts)
	}
	jump := entry.JumpHosts[0]
	if jump.Host != "10.0.0.5" || jump.User != "jumpuser" || jump.Port != 2222 || jump.KeyPath != "/keys/bastion_ed25519" {
		t.Fatalf("jump = %+v", jump)
	}
}

func TestResolveSSHConfigProxyJumpCycleRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	if err := os.WriteFile(cfg, []byte(`
Host a
  HostName 10.0.0.1
  ProxyJump b

Host b
  HostName 10.0.0.2
  ProxyJump a
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSSHConfigAlias(cfg, "a"); err == nil || !strings.Contains(err.Error(), "ciclo") {
		t.Fatalf("esperava erro de ciclo em ProxyJump, obteve %v", err)
	}
}

func TestResolveSSHConfigWildcardNegation(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	if err := os.WriteFile(cfg, []byte(`
Host *.internal !skip.internal
  User corp
  Port 2202
`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := ResolveSSHConfigAlias(cfg, "app.internal")
	if err != nil || a.User != "corp" || a.Port != 2202 {
		t.Fatalf("app.internal = %+v, %v", a, err)
	}
	b, err := ResolveSSHConfigAlias(cfg, "skip.internal")
	if err != nil || b.User != "" || b.Port != 22 {
		t.Fatalf("skip.internal não deveria casar negação: %+v, %v", b, err)
	}
}
