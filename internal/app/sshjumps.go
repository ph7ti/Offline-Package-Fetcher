package app

import (
	"fmt"
	"strconv"
	"strings"

	"opf/internal/core"
)

// SSH jump authentication modes. They are shared by GUI, TUI, CLI and the
// persisted metadata so a session can be resumed without exposing secrets.
const (
	SSHJumpAuthPassword    = "password"
	SSHJumpAuthKey         = "key"
	SSHJumpAuthKeyPassword = "key+password"
	SSHJumpAuthAgent       = "agent"
)

// SSHJumpMetadata is the non-secret description of a jump host persisted in
// sessions.config_json. Passwords are deliberately absent.
type SSHJumpMetadata struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	AuthMode   string `json:"auth_mode"`
	KeyPath    string `json:"key_path,omitempty"`
	KnownHosts string `json:"known_hosts,omitempty"`
}

// SSHJumpInput carries the editable values of one jump host, including its
// password and optional key passphrase. It is only kept in memory.
type SSHJumpInput struct {
	Host          string
	Port          string
	User          string
	AuthMode      string
	Password      string
	KeyPath       string
	KeyPassphrase string
	KnownHosts    string
}

// NormalizeSSHJumpAuthMode accepts the canonical English modes and the
// Portuguese labels used by the UI, returning the canonical value.
func NormalizeSSHJumpAuthMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SSHJumpAuthPassword, "senha":
		return SSHJumpAuthPassword, nil
	case SSHJumpAuthKey, "chave":
		return SSHJumpAuthKey, nil
	case SSHJumpAuthKeyPassword, "chave+senha", "chave + senha", "chave e senha":
		return SSHJumpAuthKeyPassword, nil
	case SSHJumpAuthAgent, "ssh-agent", "agente":
		return SSHJumpAuthAgent, nil
	default:
		return "", fmt.Errorf("modo de autenticação inválido: %q (use password, key, key+password ou agent)", value)
	}
}

// ValidateSSHJumpInput validates one jump, using index as the 1-based bridge
// number in messages. It never includes the password value in an error.
func ValidateSSHJumpInput(index int, in SSHJumpInput) error {
	bridge := fmt.Sprintf("Ponte %d", index)
	if strings.TrimSpace(in.Host) == "" {
		return fmt.Errorf("%s sem host SSH", bridge)
	}
	if strings.TrimSpace(in.User) == "" {
		return fmt.Errorf("%s sem usuário SSH", bridge)
	}
	port := baseSSHJumpPort(in.Port)
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s com porta SSH inválida", bridge)
	}
	mode, err := NormalizeSSHJumpAuthMode(in.AuthMode)
	if err != nil {
		return fmt.Errorf("%s: %w", bridge, err)
	}
	keyPath := strings.TrimSpace(in.KeyPath)
	switch mode {
	case SSHJumpAuthPassword:
		if in.Password == "" {
			return fmt.Errorf("%s exige senha", bridge)
		}
	case SSHJumpAuthKey:
		if keyPath == "" {
			return fmt.Errorf("%s exige chave privada", bridge)
		}
	case SSHJumpAuthKeyPassword:
		if keyPath == "" {
			return fmt.Errorf("%s exige chave privada", bridge)
		}
		if in.Password == "" {
			return fmt.Errorf("%s exige senha", bridge)
		}
	}
	return nil
}

// BuildSSHJumpHosts converts editable inputs into SSHConfig hops. Credentials
// are never inherited from the destination: only known_hosts (when empty) and
// InsecureSkipHostKey are copied from base.
func BuildSSHJumpHosts(base core.SSHConfig, inputs []SSHJumpInput) ([]core.SSHConfig, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	jumps := make([]core.SSHConfig, 0, len(inputs))
	for i, in := range inputs {
		if err := ValidateSSHJumpInput(i+1, in); err != nil {
			return nil, err
		}
		mode, _ := NormalizeSSHJumpAuthMode(in.AuthMode)
		keyPath := strings.TrimSpace(in.KeyPath)
		keyPassphrase := in.KeyPassphrase
		password := in.Password
		useAgent := false
		switch mode {
		case SSHJumpAuthPassword:
			keyPath = ""
			keyPassphrase = ""
		case SSHJumpAuthKey:
			password = ""
		case SSHJumpAuthAgent:
			keyPath = ""
			keyPassphrase = ""
			password = ""
			useAgent = true
		}
		knownHosts := strings.TrimSpace(in.KnownHosts)
		if knownHosts == "" {
			knownHosts = base.KnownHosts
		}
		jumps = append(jumps, core.SSHConfig{
			Host:                strings.TrimSpace(in.Host),
			Port:                baseSSHJumpPort(in.Port),
			User:                strings.TrimSpace(in.User),
			KeyPath:             keyPath,
			KeyPassphrase:       keyPassphrase,
			UseAgent:            useAgent,
			Password:            password,
			KnownHosts:          knownHosts,
			InsecureSkipHostKey: base.InsecureSkipHostKey,
		})
	}
	return jumps, nil
}

// SSHJumpMetadataFromConfigs derives persisted metadata from resolved hops.
// Passwords are never copied.
func SSHJumpMetadataFromConfigs(jumps []core.SSHConfig) []SSHJumpMetadata {
	if len(jumps) == 0 {
		return nil
	}
	out := make([]SSHJumpMetadata, 0, len(jumps))
	for _, jump := range jumps {
		port := jump.Port
		if port == 0 {
			port = 22
		}
		out = append(out, SSHJumpMetadata{
			Host:       jump.Host,
			Port:       port,
			User:       jump.User,
			AuthMode:   sshJumpAuthModeFromConfig(jump),
			KeyPath:    jump.KeyPath,
			KnownHosts: jump.KnownHosts,
		})
	}
	return out
}

// SSHJumpMetadataFromInputs derives persisted metadata from editable inputs.
// Passwords are never copied.
func SSHJumpMetadataFromInputs(inputs []SSHJumpInput) []SSHJumpMetadata {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]SSHJumpMetadata, 0, len(inputs))
	for _, in := range inputs {
		mode, err := NormalizeSSHJumpAuthMode(in.AuthMode)
		if err != nil {
			mode = strings.ToLower(strings.TrimSpace(in.AuthMode))
		}
		out = append(out, SSHJumpMetadata{
			Host:       strings.TrimSpace(in.Host),
			Port:       baseSSHJumpPort(in.Port),
			User:       strings.TrimSpace(in.User),
			AuthMode:   mode,
			KeyPath:    strings.TrimSpace(in.KeyPath),
			KnownHosts: strings.TrimSpace(in.KnownHosts),
		})
	}
	return out
}

// SSHJumpFingerprint is a stable identity for detection invalidation. It
// includes host, port, user, mode, key and known_hosts and deliberately
// excludes the password so typing a password does not invalidate detection.
func SSHJumpFingerprint(inputs []SSHJumpInput) string {
	if len(inputs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, in := range inputs {
		mode, err := NormalizeSSHJumpAuthMode(in.AuthMode)
		if err != nil {
			mode = strings.ToLower(strings.TrimSpace(in.AuthMode))
		}
		fmt.Fprintf(&b, "%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s\n",
			strings.TrimSpace(in.Host),
			baseSSHJumpPort(in.Port),
			strings.TrimSpace(in.User),
			mode,
			strings.TrimSpace(in.KeyPath),
			strings.TrimSpace(in.KnownHosts),
		)
	}
	return b.String()
}

func sshJumpAuthModeFromConfig(cfg core.SSHConfig) string {
	switch {
	case cfg.UseAgent && cfg.KeyPath == "" && cfg.Password == "":
		return SSHJumpAuthAgent
	case cfg.KeyPath != "" && cfg.Password != "":
		return SSHJumpAuthKeyPassword
	case cfg.KeyPath != "":
		return SSHJumpAuthKey
	case cfg.Password != "":
		return SSHJumpAuthPassword
	default:
		return ""
	}
}

func baseSSHJumpPort(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 22
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return port
}
