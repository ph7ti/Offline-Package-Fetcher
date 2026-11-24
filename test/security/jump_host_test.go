package security

import (
	"context"
	"strings"
	"testing"

	"opf/internal/app"
	"opf/internal/core"
	"opf/internal/transfer"
)

// TestSSHJumpInputValidation covers malformed jump definitions, including port
// and auth-mode injection, and asserts secrets never appear in error messages.
func TestSSHJumpInputValidation(t *testing.T) {
	const secret = "dummy-jump-password"
	tests := []struct {
		name    string
		input   app.SSHJumpInput
		wantErr string
	}{
		{
			name:    "missing host",
			input:   app.SSHJumpInput{User: "u", AuthMode: app.SSHJumpAuthPassword, Password: secret},
			wantErr: "sem host SSH",
		},
		{
			name:    "missing user",
			input:   app.SSHJumpInput{Host: "h", AuthMode: app.SSHJumpAuthPassword, Password: secret},
			wantErr: "sem usuário SSH",
		},
		{
			name:    "port injection",
			input:   app.SSHJumpInput{Host: "h", User: "u", Port: "22;id", AuthMode: app.SSHJumpAuthPassword, Password: secret},
			wantErr: "porta SSH inválida",
		},
		{
			name:    "port out of range",
			input:   app.SSHJumpInput{Host: "h", User: "u", Port: "70000", AuthMode: app.SSHJumpAuthPassword, Password: secret},
			wantErr: "porta SSH inválida",
		},
		{
			name:    "auth mode injection",
			input:   app.SSHJumpInput{Host: "h", User: "u", AuthMode: "password;id", Password: secret},
			wantErr: "modo de autenticação inválido",
		},
		{
			name:    "password mode without password",
			input:   app.SSHJumpInput{Host: "h", User: "u", AuthMode: app.SSHJumpAuthPassword},
			wantErr: "exige senha",
		},
		{
			name:    "key mode without key",
			input:   app.SSHJumpInput{Host: "h", User: "u", AuthMode: app.SSHJumpAuthKey},
			wantErr: "exige chave privada",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := app.ValidateSSHJumpInput(1, tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("password leaked in validation error: %v", err)
			}
		})
	}
}

// TestSSHJumpControlCharactersRejectedBeforeDial proves hostile jump metadata
// cannot reach the network stack: validation must fail before any TCP dial.
func TestSSHJumpControlCharactersRejectedBeforeDial(t *testing.T) {
	for _, host := range []string{"evil\nhost", "evil\rhost", "evil\x00host"} {
		cfg := core.SSHConfig{
			Host: "destino", Port: 22, User: "opf",
			KeyPath: "/tmp/dummy-key", KnownHosts: "/tmp/dummy-known-hosts",
			JumpHosts: []core.SSHConfig{{
				Host: host, Port: 22, User: "bridge", KeyPath: "/tmp/dummy-key",
			}},
		}
		manager := transfer.NewSSHTransferManager()
		err := manager.Connect(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), "host SSH inválido") {
			t.Fatalf("host %q: error = %v, want control-character rejection", host, err)
		}
	}
	for _, host := range []string{"dest\nino", "dest\x00ino"} {
		manager := transfer.NewSSHTransferManager()
		err := manager.Connect(context.Background(), core.SSHConfig{
			Host: host, Port: 22, User: "opf", KeyPath: "/tmp/dummy-key",
		})
		if err == nil || !strings.Contains(err.Error(), "host SSH inválido") {
			t.Fatalf("destination %q: error = %v", host, err)
		}
	}
}

// TestSSHJumpRequiresCredentials proves a jump without key or password is
// rejected before dialing the destination.
func TestSSHJumpRequiresCredentials(t *testing.T) {
	manager := transfer.NewSSHTransferManager()
	err := manager.Connect(context.Background(), core.SSHConfig{
		Host: "destino", Port: 22, User: "opf", KeyPath: "/tmp/dummy-key",
		JumpHosts: []core.SSHConfig{{Host: "bridge", Port: 22, User: "bridge"}},
	})
	if err == nil || !strings.Contains(err.Error(), "sem chave, ssh-agent nem senha SSH") {
		t.Fatalf("error = %v", err)
	}
}

// TestSSHJumpBuildKeepsSecretsInMemoryOnly asserts the resolved hop carries the
// credentials needed for the handshake while persisted metadata never does.
func TestSSHJumpBuildKeepsSecretsInMemoryOnly(t *testing.T) {
	const secret = "dummy-jump-password"
	base := core.SSHConfig{KnownHosts: "/tmp/dest-known-hosts"}
	jumps, err := app.BuildSSHJumpHosts(base, []app.SSHJumpInput{{
		Host: "bridge", Port: "2200", User: "bridge",
		AuthMode: app.SSHJumpAuthKeyPassword, KeyPath: "/tmp/bridge-key", Password: secret,
		KnownHosts: "/tmp/bridge-known-hosts",
	}})
	if err != nil {
		t.Fatalf("BuildSSHJumpHosts: %v", err)
	}
	if len(jumps) != 1 || jumps[0].Password != secret {
		t.Fatalf("in-memory hop missing credential: %+v", jumps)
	}
	metadata := app.SSHJumpMetadataFromConfigs(jumps)
	if len(metadata) != 1 {
		t.Fatalf("metadata = %+v", metadata)
	}
	encoded := mustJSON(t, metadata)
	if strings.Contains(encoded, secret) {
		t.Fatalf("password persisted in jump metadata: %s", encoded)
	}
	if !strings.Contains(encoded, `"auth_mode":"key+password"`) {
		t.Fatalf("metadata missing auth mode: %s", encoded)
	}
}
