package app

import (
	"encoding/json"
	"strings"
	"testing"

	"opf/internal/core"
)

func TestBuildSSHJumpHostsModes(t *testing.T) {
	base := core.SSHConfig{
		Host: "dest", Port: 22, User: "opf",
		KeyPath: "/dest/key", Password: "dest-pass",
		KnownHosts: "/tmp/known_hosts", InsecureSkipHostKey: true,
	}

	tests := []struct {
		name     string
		input    SSHJumpInput
		wantKey  string
		wantPass string
	}{
		{
			name: "senha",
			input: SSHJumpInput{
				Host: "a", User: "u1", Port: "2200", AuthMode: SSHJumpAuthPassword,
				Password: "pw1", KeyPath: "/ignored",
			},
			wantPass: "pw1",
		},
		{
			name: "chave",
			input: SSHJumpInput{
				Host: "b", User: "u2", AuthMode: SSHJumpAuthKey,
				KeyPath: "/jump/key", Password: "ignored",
			},
			wantKey: "/jump/key",
		},
		{
			name: "chave e senha",
			input: SSHJumpInput{
				Host: "c", User: "u3", AuthMode: SSHJumpAuthKeyPassword,
				KeyPath: "/jump/key", Password: "pw3",
			},
			wantKey:  "/jump/key",
			wantPass: "pw3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jumps, err := BuildSSHJumpHosts(base, []SSHJumpInput{tt.input})
			if err != nil {
				t.Fatalf("BuildSSHJumpHosts: %v", err)
			}
			if len(jumps) != 1 {
				t.Fatalf("jumps = %d", len(jumps))
			}
			jump := jumps[0]
			if jump.Host != tt.input.Host || jump.User != tt.input.User {
				t.Fatalf("jump = %+v", jump)
			}
			if jump.KeyPath != tt.wantKey || jump.Password != tt.wantPass {
				t.Fatalf("credenciais = key %q pass %q", jump.KeyPath, jump.Password)
			}
			if jump.KnownHosts != base.KnownHosts {
				t.Fatalf("known_hosts não herdado do destino: %q", jump.KnownHosts)
			}
			if !jump.InsecureSkipHostKey {
				t.Fatal("InsecureSkipHostKey não copiado do destino")
			}
			if len(jump.JumpHosts) != 0 {
				t.Fatalf("jump não pode ser aninhado: %+v", jump.JumpHosts)
			}
		})
	}
}

func TestBuildSSHJumpHostsDefaultPortAndExplicitKnownHosts(t *testing.T) {
	base := core.SSHConfig{KnownHosts: "/dest/known_hosts"}
	jumps, err := BuildSSHJumpHosts(base, []SSHJumpInput{{
		Host: "bridge", User: "bridge", AuthMode: SSHJumpAuthKey,
		KeyPath: "/bridge/key", KnownHosts: "/bridge/known_hosts",
	}})
	if err != nil {
		t.Fatalf("BuildSSHJumpHosts: %v", err)
	}
	if jumps[0].Port != 22 {
		t.Fatalf("porta padrão = %d", jumps[0].Port)
	}
	if jumps[0].KnownHosts != "/bridge/known_hosts" {
		t.Fatalf("known_hosts = %q", jumps[0].KnownHosts)
	}
}

func TestBuildSSHJumpHostsValidation(t *testing.T) {
	tests := []struct {
		name      string
		input     SSHJumpInput
		wantError string
		secret    string
	}{
		{"sem host", SSHJumpInput{User: "u", AuthMode: SSHJumpAuthPassword, Password: "pw"}, "Ponte 1 sem host SSH", "pw"},
		{"sem usuário", SSHJumpInput{Host: "h", AuthMode: SSHJumpAuthPassword, Password: "pw"}, "Ponte 1 sem usuário SSH", "pw"},
		{"senha ausente", SSHJumpInput{Host: "h", User: "u", AuthMode: SSHJumpAuthPassword}, "Ponte 1 exige senha", ""},
		{"chave ausente", SSHJumpInput{Host: "h", User: "u", AuthMode: SSHJumpAuthKey}, "Ponte 1 exige chave privada", ""},
		{"chave e senha sem chave", SSHJumpInput{Host: "h", User: "u", AuthMode: SSHJumpAuthKeyPassword, Password: "segredo"}, "Ponte 1 exige chave privada", "segredo"},
		{"chave e senha sem senha", SSHJumpInput{Host: "h", User: "u", AuthMode: SSHJumpAuthKeyPassword, KeyPath: "/k"}, "Ponte 1 exige senha", ""},
		{"porta inválida", SSHJumpInput{Host: "h", User: "u", Port: "abc", AuthMode: SSHJumpAuthPassword, Password: "pw"}, "porta SSH inválida", "pw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildSSHJumpHosts(core.SSHConfig{}, []SSHJumpInput{tt.input})
			if err == nil {
				t.Fatal("esperava erro")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("erro = %q, esperava %q", err, tt.wantError)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Fatalf("erro vazou senha: %q", err)
			}
		})
	}
}

func TestSSHJumpMetadataExcludesPassword(t *testing.T) {
	inputs := []SSHJumpInput{
		{Host: "a", User: "u1", AuthMode: SSHJumpAuthKeyPassword, KeyPath: "/k", KnownHosts: "/kh", Password: "segredo-1"},
		{Host: "b", User: "u2", AuthMode: SSHJumpAuthPassword, Password: "segredo-2"},
	}
	metadata := SSHJumpMetadataFromInputs(inputs)
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(data)
	for _, secret := range []string{"segredo-1", "segredo-2"} {
		if strings.Contains(text, secret) {
			t.Fatalf("metadados vazaram senha: %s", text)
		}
	}
	if !strings.Contains(text, `"auth_mode":"key+password"`) {
		t.Fatalf("modo não persistido: %s", text)
	}
}

func TestSSHJumpFingerprintIgnoresPassword(t *testing.T) {
	base := []SSHJumpInput{{Host: "a", User: "u", AuthMode: SSHJumpAuthPassword, Password: "one"}}
	changed := []SSHJumpInput{{Host: "a", User: "u", AuthMode: SSHJumpAuthPassword, Password: "two"}}
	if SSHJumpFingerprint(base) != SSHJumpFingerprint(changed) {
		t.Fatal("senha não deveria alterar o fingerprint")
	}

	mutations := []SSHJumpInput{
		{Host: "b", User: "u", AuthMode: SSHJumpAuthPassword, Password: "one"},
		{Host: "a", User: "v", AuthMode: SSHJumpAuthPassword, Password: "one"},
		{Host: "a", User: "u", AuthMode: SSHJumpAuthKey, KeyPath: "/k"},
		{Host: "a", User: "u", AuthMode: SSHJumpAuthPassword, KnownHosts: "/kh", Password: "one"},
	}
	for _, mutation := range mutations {
		if SSHJumpFingerprint(base) == SSHJumpFingerprint([]SSHJumpInput{mutation}) {
			t.Fatalf("fingerprint deveria mudar para %+v", mutation)
		}
	}
}

func TestConfigJSONPersistsOnlyJumpMetadata(t *testing.T) {
	service := &Service{cfg: Config{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		Packages:      []string{"vim"},
		SSH: core.SSHConfig{
			Host: "dest", Port: 22, User: "opf", Password: "destino-segredo",
			JumpHosts: []core.SSHConfig{
				{Host: "jump", Port: 2200, User: "salto", Password: "jump-segredo"},
				{Host: "jump2", Port: 22, User: "salto2", KeyPath: "/k", Password: "jump2-segredo"}, // gitleaks:allow -- dummy fixture password
			},
		},
	}}

	config := service.configJSON()
	for _, secret := range []string{"destino-segredo", "jump-segredo", "jump2-segredo"} {
		if strings.Contains(config, secret) {
			t.Fatalf("config_json vazou segredo %q: %s", secret, config)
		}
	}
	var payload struct {
		Jumps []SSHJumpMetadata `json:"ssh_jumps"`
	}
	if err := json.Unmarshal([]byte(config), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Jumps) != 2 {
		t.Fatalf("ssh_jumps = %+v", payload.Jumps)
	}
	if payload.Jumps[0].Host != "jump" || payload.Jumps[0].AuthMode != SSHJumpAuthPassword {
		t.Fatalf("jump 0 = %+v", payload.Jumps[0])
	}
	if payload.Jumps[1].AuthMode != SSHJumpAuthKeyPassword {
		t.Fatalf("jump 1 = %+v", payload.Jumps[1])
	}
}

func TestConfigJSONPersistsRepositories(t *testing.T) {
	service := &Service{cfg: Config{
		TargetOS: "rockylinux", TargetVersion: "9", TargetArch: "x86_64",
		Repositories: []core.PackageRepository{{
			Name: "Interno", Family: core.RepositoryFamilyRPM,
			URL: "https://repo.interno/el9/$basearch", KeyFile: "/tmp/RPM-GPG-KEY-interno",
			CAFile: "/tmp/ca.pem",
		}},
	}}
	config := service.configJSON()
	for _, want := range []string{`"repositories"`, `repo.interno`, `"key_file"`, `"ca_file"`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config_json sem %q: %s", want, config)
		}
	}
	var payload struct {
		Repositories []core.PackageRepository `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(config), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Repositories) != 1 || payload.Repositories[0].Name != "Interno" {
		t.Fatalf("repositories = %+v", payload.Repositories)
	}
}

func TestNewRejectsRepositoryFamilyMismatch(t *testing.T) {
	_, err := New(Config{
		TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64",
		StorePath: "nao-usado.db",
		Repositories: []core.PackageRepository{{
			Name: "RPM", Family: core.RepositoryFamilyRPM, URL: "https://repo.interno/rpm", KeyFile: "/k",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "não corresponde ao alvo") {
		t.Fatalf("erro = %v", err)
	}
}
