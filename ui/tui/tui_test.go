package tui

import (
	"strings"
	"testing"

	opfapp "opf/internal/app"
	"opf/internal/core"
)

func TestSplitList(t *testing.T) {
	got := splitList("vim, curl\nvim,,bash")
	want := []string{"vim", "curl", "bash"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("splitList = %v, esperava %v", got, want)
	}
}

func TestBuildJumpHostsModes(t *testing.T) {
	base := core.SSHConfig{
		Host: "dest", Port: 2222, User: "opf",
		KeyPath: "/dest/key", KnownHosts: "/tmp/known_hosts", InsecureSkipHostKey: true,
	}
	m := &Model{jumpDrafts: []jumpDraft{
		{host: "a", port: "2200", user: "u1", authMode: opfapp.SSHJumpAuthPassword, password: "pw1", keyPath: "/ignored"},
		{host: "b", user: "u2", authMode: opfapp.SSHJumpAuthKey, keyPath: "/jump/key", password: "ignored"},
		{host: "c", port: "22", user: "u3", authMode: opfapp.SSHJumpAuthKeyPassword, keyPath: "/jump/key", password: "pw3"},
	}}

	jumps, err := m.buildJumpHosts(base)
	if err != nil {
		t.Fatalf("buildJumpHosts: %v", err)
	}
	if len(jumps) != 3 {
		t.Fatalf("jumps = %d", len(jumps))
	}
	if jumps[0].Password != "pw1" || jumps[0].KeyPath != "" || jumps[0].Port != 2200 {
		t.Fatalf("jump 0 = %+v", jumps[0])
	}
	if jumps[1].KeyPath != "/jump/key" || jumps[1].Password != "" || jumps[1].Port != 22 {
		t.Fatalf("jump 1 = %+v", jumps[1])
	}
	if jumps[2].KeyPath != "/jump/key" || jumps[2].Password != "pw3" {
		t.Fatalf("jump 2 = %+v", jumps[2])
	}
	for _, jump := range jumps {
		if jump.KnownHosts != base.KnownHosts || !jump.InsecureSkipHostKey {
			t.Fatalf("known_hosts/host key não herdados: %+v", jump)
		}
	}
}

func TestBuildJumpHostsIndexedErrors(t *testing.T) {
	m := &Model{jumpDrafts: []jumpDraft{
		{host: "a", port: "22", user: "u1", authMode: opfapp.SSHJumpAuthPassword, password: "pw1"},
		{host: "b", port: "22", user: "u2", authMode: opfapp.SSHJumpAuthPassword},
	}}
	if _, err := m.buildJumpHosts(core.SSHConfig{}); err == nil || !strings.Contains(err.Error(), "Ponte 2 exige senha") {
		t.Fatalf("erro = %v", err)
	}
}

func TestJumpFingerprintIgnoresPassword(t *testing.T) {
	m := &Model{jumpDrafts: []jumpDraft{{host: "a", port: "22", user: "u", authMode: opfapp.SSHJumpAuthPassword, password: "one"}}}
	before := m.jumpFingerprint()
	m.jumpDrafts[0].password = "two"
	if m.jumpFingerprint() != before {
		t.Fatal("senha não deveria alterar o fingerprint")
	}
	m.jumpDrafts[0].authMode = opfapp.SSHJumpAuthKey
	m.jumpDrafts[0].keyPath = "/k"
	if m.jumpFingerprint() == before {
		t.Fatal("modo/chave deveria alterar o fingerprint")
	}
}

func TestJumpDraftMetadataRoundTripWithoutPassword(t *testing.T) {
	draft := jumpDraft{
		host: "bridge", port: "2200", user: "salto",
		authMode: opfapp.SSHJumpAuthKeyPassword, password: "segredo",
		keyPath: "/jump/key", knownHosts: "/jump/known_hosts",
	}
	meta := draft.metadata()
	if meta.AuthMode != opfapp.SSHJumpAuthKeyPassword {
		t.Fatalf("auth_mode = %q", meta.AuthMode)
	}

	restored := newJumpDraft()
	restored.setMetadata(meta)
	if restored.password != "" {
		t.Fatalf("senha restaurada indevidamente: %q", restored.password)
	}
	if restored.host != "bridge" || restored.port != "2200" || restored.user != "salto" {
		t.Fatalf("metadados não restaurados: %+v", restored)
	}
	if restored.keyPath != "/jump/key" || restored.knownHosts != "/jump/known_hosts" {
		t.Fatalf("chave/known_hosts não restaurados: %+v", restored)
	}
}

func TestMoveAndRemoveJumpDrafts(t *testing.T) {
	m := &Model{jumpDrafts: []jumpDraft{
		{host: "a", authMode: opfapp.SSHJumpAuthPassword},
		{host: "b", authMode: opfapp.SSHJumpAuthPassword},
		{host: "c", authMode: opfapp.SSHJumpAuthPassword},
	}}
	m.moveJumpDraft(0, 1)
	if m.jumpDrafts[0].host != "b" || m.jumpDrafts[1].host != "a" || m.jumpIdx != 1 {
		t.Fatalf("após mover: %+v idx=%d", m.jumpDrafts, m.jumpIdx)
	}
	m.jumpIdx = 1
	m.removeJumpDraft(1)
	if len(m.jumpDrafts) != 2 || m.jumpDrafts[1].host != "c" || m.jumpIdx != 1 {
		t.Fatalf("após remover: %+v idx=%d", m.jumpDrafts, m.jumpIdx)
	}
}

func TestViewJumpsListsSummaries(t *testing.T) {
	m := &Model{jumpDrafts: []jumpDraft{
		{host: "bridge", port: "2200", user: "salto", authMode: opfapp.SSHJumpAuthKey},
	}}
	view := m.viewJumps()
	if !strings.Contains(view, "salto@bridge:2200 • chave") {
		t.Fatalf("view inesperada: %s", view)
	}
	if !strings.Contains(view, "adicionar") || !strings.Contains(view, "editar") {
		t.Fatalf("atalhos ausentes: %s", view)
	}
}

func TestWelcomeView(t *testing.T) {
	m := &Model{screen: screenWelcome}
	view := m.View()
	if !strings.Contains(view, "Offline Package Fetcher") || !strings.Contains(view, "Nova sessão") {
		t.Fatalf("view inesperada: %s", view)
	}
}

func TestSetJumpMetadataRepopulatesWithoutPassword(t *testing.T) {
	m := &Model{}
	needsPassword := m.setJumpMetadata([]opfapp.SSHJumpMetadata{
		{Host: "a", Port: 2200, User: "u1", AuthMode: opfapp.SSHJumpAuthPassword},
		{Host: "b", Port: 22, User: "u2", AuthMode: opfapp.SSHJumpAuthKey, KeyPath: "/k"},
	})
	if !needsPassword {
		t.Fatal("ponte por senha deveria exigir reautenticação")
	}
	if len(m.jumpDrafts) != 2 {
		t.Fatalf("jumpDrafts = %d", len(m.jumpDrafts))
	}
	for i, draft := range m.jumpDrafts {
		if draft.password != "" {
			t.Fatalf("ponte %d restaurou senha: %q", i+1, draft.password)
		}
	}
	if m.jumpDrafts[0].host != "a" || m.jumpDrafts[0].port != "2200" || m.jumpDrafts[0].user != "u1" {
		t.Fatalf("ponte 0 = %+v", m.jumpDrafts[0])
	}
	if m.jumpDrafts[1].keyPath != "/k" || m.jumpDrafts[1].authMode != opfapp.SSHJumpAuthKey {
		t.Fatalf("ponte 1 = %+v", m.jumpDrafts[1])
	}
}
