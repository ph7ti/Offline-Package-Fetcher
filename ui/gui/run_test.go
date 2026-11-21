//go:build cgo

package gui

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	opfapp "opf/internal/app"
	"opf/internal/core"
	"opf/internal/i18n"
)

func TestMain(m *testing.M) {
	// The suite asserts Portuguese source strings; pin the interface language so
	// the result does not depend on the machine locale.
	i18n.Set(i18n.PTBR)
	// Fill Fyne's own missing pt-BR/es strings, as Run does, to avoid noisy
	// "Translation failure" logs while the widgets render.
	initFyneTranslations()
	test.NewApp()
	os.Exit(m.Run())
}

func jumpBase() core.SSHConfig {
	return core.SSHConfig{
		Host: "dest", Port: 22, User: "opf",
		KeyPath: "/dest/key", Password: "dest-pass",
		KnownHosts: "/tmp/known_hosts", InsecureSkipHostKey: true,
	}
}

func TestJumpRowConfigModes(t *testing.T) {
	tests := []struct {
		name     string
		auth     string
		password string
		key      string
		wantKey  string
		wantPass string
	}{
		{"senha", "Senha", "pw", "/ignored", "", "pw"},
		{"chave", "Chave", "ignored", "/jump/key", "/jump/key", ""},
		{"chave e senha", "Chave + senha", "pw", "/jump/key", "/jump/key", "pw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := newJumpRow()
			row.host.SetText("bridge")
			row.port.SetText("2200")
			row.user.SetText("salto")
			row.auth.SetSelected(tt.auth)
			row.password.SetText(tt.password)
			row.keyPath.SetText(tt.key)

			cfg, err := row.config(jumpBase())
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			if cfg.Host != "bridge" || cfg.Port != 2200 || cfg.User != "salto" {
				t.Fatalf("jump = %+v", cfg)
			}
			if cfg.KeyPath != tt.wantKey || cfg.Password != tt.wantPass {
				t.Fatalf("credenciais = key %q pass %q", cfg.KeyPath, cfg.Password)
			}
			if cfg.KnownHosts != "/tmp/known_hosts" || !cfg.InsecureSkipHostKey {
				t.Fatalf("known_hosts/host key não herdados: %+v", cfg)
			}
		})
	}
}

func TestJumpRowMissingCredentials(t *testing.T) {
	row := newJumpRow()
	row.host.SetText("bridge")
	row.user.SetText("salto")
	row.auth.SetSelected("Chave + senha")
	row.password.SetText("segredo")

	if _, err := row.config(core.SSHConfig{}); err == nil || !strings.Contains(err.Error(), "exige chave privada") {
		t.Fatalf("erro = %v", err)
	} else if strings.Contains(err.Error(), "segredo") {
		t.Fatalf("erro vazou senha: %v", err)
	}

	row.auth.SetSelected("Senha")
	row.password.SetText("")
	if _, err := row.config(core.SSHConfig{}); err == nil || !strings.Contains(err.Error(), "exige senha") {
		t.Fatalf("erro = %v", err)
	}
}

func TestJumpRowsBuildConfigsWithIndexedErrors(t *testing.T) {
	u := &UI{}
	first := newJumpRow()
	first.host.SetText("bridge1")
	first.user.SetText("u1")
	first.auth.SetSelected("Senha")
	first.password.SetText("pw1")
	second := newJumpRow()
	second.host.SetText("bridge2")
	second.user.SetText("u2")
	second.auth.SetSelected("Senha")
	u.jumpRows = []*jumpRow{first, second}

	if _, err := u.buildJumpConfigs(core.SSHConfig{}); err == nil || !strings.Contains(err.Error(), "Ponte 2 exige senha") {
		t.Fatalf("erro = %v", err)
	}
}

func TestJumpFingerprintIgnoresPassword(t *testing.T) {
	u := &UI{}
	row := newJumpRow()
	row.host.SetText("bridge")
	row.port.SetText("2200")
	row.user.SetText("salto")
	row.auth.SetSelected("Senha")
	row.password.SetText("one")
	u.jumpRows = []*jumpRow{row}

	before := u.jumpFingerprint()
	row.password.SetText("two")
	if u.jumpFingerprint() != before {
		t.Fatal("senha não deveria alterar o fingerprint")
	}

	row.host.SetText("other")
	if u.jumpFingerprint() == before {
		t.Fatal("host deveria alterar o fingerprint")
	}
}

func TestJumpRowMetadataRoundTripWithoutPassword(t *testing.T) {
	source := newJumpRow()
	source.host.SetText("bridge")
	source.port.SetText("2200")
	source.user.SetText("salto")
	source.auth.SetSelected("Chave + senha")
	source.password.SetText("segredo")
	source.keyPath.SetText("/jump/key")
	source.knownHosts.SetText("/jump/known_hosts")

	meta := source.metadata()
	if meta.AuthMode != opfapp.SSHJumpAuthKeyPassword {
		t.Fatalf("auth_mode = %q", meta.AuthMode)
	}

	target := newJumpRow()
	target.setMetadata(meta)
	if target.password.Text != "" {
		t.Fatalf("senha restaurada indevidamente: %q", target.password.Text)
	}
	if target.host.Text != "bridge" || target.port.Text != "2200" || target.user.Text != "salto" {
		t.Fatalf("metadados não restaurados: %+v", target.input())
	}
	if target.keyPath.Text != "/jump/key" || target.knownHosts.Text != "/jump/known_hosts" {
		t.Fatalf("chave/known_hosts não restaurados: %+v", target.input())
	}
}

func TestAddRemoveMoveJumpRows(t *testing.T) {
	u := &UI{}
	u.addJumpRow()
	u.addJumpRow()
	u.addJumpRow()
	u.jumpRows[0].host.SetText("a")
	u.jumpRows[1].host.SetText("b")
	u.jumpRows[2].host.SetText("c")

	u.moveJumpRow(0, 1)
	if got := jumpHosts(u); got != "b,a,c" {
		t.Fatalf("após mover = %s", got)
	}

	u.removeJumpRow(1)
	if got := jumpHosts(u); got != "b,c" {
		t.Fatalf("após remover = %s", got)
	}

	u.clearJumpRows()
	if len(u.jumpRows) != 0 {
		t.Fatalf("jumpRows = %d", len(u.jumpRows))
	}
}

func jumpHosts(u *UI) string {
	var hosts []string
	for _, row := range u.jumpRows {
		hosts = append(hosts, row.host.Text)
	}
	return strings.Join(hosts, ",")
}

func TestSetJumpMetadataRepopulatesWithoutPassword(t *testing.T) {
	u := &UI{}
	needsPassword := u.setJumpMetadata([]opfapp.SSHJumpMetadata{
		{Host: "a", Port: 2200, User: "u1", AuthMode: opfapp.SSHJumpAuthPassword},
		{Host: "b", Port: 22, User: "u2", AuthMode: opfapp.SSHJumpAuthKey, KeyPath: "/k"},
	})
	if !needsPassword {
		t.Fatal("ponte por senha deveria exigir reautenticação")
	}
	if len(u.jumpRows) != 2 {
		t.Fatalf("jumpRows = %d", len(u.jumpRows))
	}
	for i, row := range u.jumpRows {
		if row.password.Text != "" {
			t.Fatalf("ponte %d restaurou senha: %q", i+1, row.password.Text)
		}
	}
	if u.jumpRows[0].host.Text != "a" || u.jumpRows[0].port.Text != "2200" || u.jumpRows[0].user.Text != "u1" {
		t.Fatalf("ponte 0 = %+v", u.jumpRows[0].input())
	}
	if u.jumpRows[1].keyPath.Text != "/k" || u.jumpRows[1].auth.Selected != "Chave" {
		t.Fatalf("ponte 1 = %+v", u.jumpRows[1].input())
	}
}

func TestBuildSSHConfigPropagatesSkipHostKey(t *testing.T) {
	u := &UI{
		sshHost:     widget.NewEntry(),
		sshPort:     widget.NewEntry(),
		sshUser:     widget.NewEntry(),
		sshKey:      widget.NewEntry(),
		sshPassword: widget.NewPasswordEntry(),
		knownHosts:  widget.NewEntry(),
		skipHostKey: widget.NewCheck("", nil),
	}
	u.sshHost.SetText("dest")
	u.sshPort.SetText("22")
	u.sshUser.SetText("opf")
	u.sshKey.SetText("/tmp/chave-inexistente")
	u.knownHosts.SetText("/tmp/known_hosts-inexistente")
	u.skipHostKey.SetChecked(true)

	jump := newJumpRow()
	jump.host.SetText("bridge")
	jump.user.SetText("salto")
	jump.auth.SetSelected("Senha")
	jump.password.SetText("pw")
	u.jumpRows = []*jumpRow{jump}

	cfg, err := u.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig: %v", err)
	}
	if !cfg.InsecureSkipHostKey {
		t.Fatal("InsecureSkipHostKey não propagado ao destino")
	}
	if len(cfg.JumpHosts) != 1 || !cfg.JumpHosts[0].InsecureSkipHostKey {
		t.Fatalf("InsecureSkipHostKey não propagado à ponte: %+v", cfg.JumpHosts)
	}
}
