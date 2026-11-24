package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"opf/internal/core"
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// ProfileJump stores non-secret jump-host metadata inside a saved profile.
// Passwords and passphrases are never persisted.
type ProfileJump struct {
	Host       string `json:"host"`
	Port       int    `json:"port,omitempty"`
	User       string `json:"user"`
	AuthMode   string `json:"auth_mode,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	KnownHosts string `json:"known_hosts,omitempty"`
}

// Profile is a reusable non-secret session preset stored in profiles.json
// (mode 0600). Secrets (SSH passwords, sudo passwords, key passphrases, proxy
// credentials and private repository credentials) are strictly excluded.
type Profile struct {
	Name            string                   `json:"name"`
	TargetOS        string                   `json:"target_os,omitempty"`
	TargetVersion   string                   `json:"target_version,omitempty"`
	TargetArch      string                   `json:"target_arch,omitempty"`
	FetchMethod     string                   `json:"fetch_method,omitempty"`
	ContainerHost   string                   `json:"container_host,omitempty"`
	Image           string                   `json:"image,omitempty"`
	WSLDistro       string                   `json:"wsl_distro,omitempty"`
	IncludeOptional bool                     `json:"include_optional,omitempty"`
	ForceReinstall  bool                     `json:"force_reinstall,omitempty"`
	OfflineBundle   bool                     `json:"offline_bundle,omitempty"`
	SSHHost         string                   `json:"ssh_host,omitempty"`
	SSHPort         int                      `json:"ssh_port,omitempty"`
	SSHUser         string                   `json:"ssh_user,omitempty"`
	SSHKeyPath      string                   `json:"ssh_key_path,omitempty"`
	SSHUseAgent     bool                     `json:"ssh_use_agent,omitempty"`
	SSHKnownHosts   string                   `json:"ssh_known_hosts,omitempty"`
	RemoteDir       string                   `json:"remote_dir,omitempty"`
	SSHJumps        []ProfileJump            `json:"ssh_jumps,omitempty"`
	Repositories    []core.PackageRepository `json:"repositories,omitempty"`
	Packages        []string                 `json:"packages,omitempty"`
}

// DefaultProfilesPath returns <UserConfigDir>/opf/profiles.json.
func DefaultProfilesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolver diretório de perfis: %w", err)
	}
	return filepath.Join(dir, "opf", "profiles.json"), nil
}

// Validate checks a saved profile for syntax, safe bounds and absence of
// invalid/secret-bearing fields.
func (p Profile) Validate() error {
	name := strings.TrimSpace(p.Name)
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("nome de perfil inválido: %q (use letras, números, espaço, ponto, hífen ou sublinhado)", p.Name)
	}
	switch strings.ToLower(strings.TrimSpace(p.FetchMethod)) {
	case "", "docker", "podman", "wsl", "auto":
	default:
		return fmt.Errorf("método de fetch inválido no perfil %q: %q", name, p.FetchMethod)
	}
	if p.SSHPort < 0 || p.SSHPort > 65535 {
		return fmt.Errorf("porta SSH inválida no perfil %q: %d", name, p.SSHPort)
	}
	for i, jump := range p.SSHJumps {
		if strings.TrimSpace(jump.Host) == "" {
			return fmt.Errorf("perfil %q: ponte %d sem host", name, i+1)
		}
		if jump.Port < 0 || jump.Port > 65535 {
			return fmt.Errorf("perfil %q: ponte %d com porta inválida: %d", name, i+1, jump.Port)
		}
	}
	if len(p.Repositories) > 0 && strings.TrimSpace(p.TargetOS) != "" {
		family, err := core.FamilyForTargetOS(p.TargetOS)
		if err != nil {
			return fmt.Errorf("perfil %q: %w", name, err)
		}
		if err := core.ValidatePackageRepositories(p.Repositories, family); err != nil {
			return fmt.Errorf("perfil %q: %w", name, err)
		}
	}
	return nil
}

// Sanitized returns a copy of Profile with trimmed strings and all ephemeral
// repository auth fields cleared.
func (p Profile) Sanitized() Profile {
	out := p
	out.Name = strings.TrimSpace(p.Name)
	out.TargetOS = strings.ToLower(strings.TrimSpace(p.TargetOS))
	out.TargetVersion = strings.TrimSpace(p.TargetVersion)
	out.TargetArch = strings.TrimSpace(p.TargetArch)
	out.FetchMethod = strings.ToLower(strings.TrimSpace(p.FetchMethod))
	out.ContainerHost = strings.TrimSpace(p.ContainerHost)
	out.Image = strings.TrimSpace(p.Image)
	out.WSLDistro = strings.TrimSpace(p.WSLDistro)
	out.SSHHost = strings.TrimSpace(p.SSHHost)
	out.SSHUser = strings.TrimSpace(p.SSHUser)
	out.SSHKeyPath = strings.TrimSpace(p.SSHKeyPath)
	out.SSHKnownHosts = strings.TrimSpace(p.SSHKnownHosts)
	out.RemoteDir = strings.TrimSpace(p.RemoteDir)
	if len(p.Repositories) > 0 {
		repos := make([]core.PackageRepository, len(p.Repositories))
		for i, r := range p.Repositories {
			r.AuthType = ""
			r.AuthUsername = ""
			r.AuthPassword = ""
			r.AuthToken = ""
			repos[i] = r
		}
		out.Repositories = repos
	}
	return out
}

// LoadProfiles reads all saved profiles sorted by name. A missing file returns
// an empty slice without error.
func LoadProfiles(path string) ([]Profile, error) {
	if path == "" {
		var err error
		path, err = DefaultProfilesPath()
		if err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler perfis %q: %w", path, err)
	}
	var profiles []Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("interpretar perfis %q: %w", path, err)
	}
	for i := range profiles {
		profiles[i] = profiles[i].Sanitized()
		if err := profiles[i].Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	return profiles, nil
}

// SaveProfiles atomically writes profiles to disk with mode 0600.
func SaveProfiles(path string, profiles []Profile) error {
	if path == "" {
		var err error
		path, err = DefaultProfilesPath()
		if err != nil {
			return err
		}
	}
	cleaned := make([]Profile, 0, len(profiles))
	seen := make(map[string]struct{}, len(profiles))
	for _, p := range profiles {
		p = p.Sanitized()
		if err := p.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(p.Name)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("perfil duplicado: %q", p.Name)
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, p)
	}
	sort.Slice(cleaned, func(i, j int) bool {
		return strings.ToLower(cleaned[i].Name) < strings.ToLower(cleaned[j].Name)
	})

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("criar diretório de perfis: %w", err)
	}
	data, err := json.MarshalIndent(cleaned, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar perfis: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("criar arquivo temporário de perfis: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("ajustar permissões dos perfis: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("escrever perfis: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sincronizar perfis: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechar perfis: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publicar perfis: %w", err)
	}
	return nil
}

// GetProfile finds a profile by case-insensitive name.
func GetProfile(path, name string) (*Profile, error) {
	profiles, err := LoadProfiles(path)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(name)
	for i := range profiles {
		if strings.EqualFold(profiles[i].Name, target) {
			copyProfile := profiles[i]
			return &copyProfile, nil
		}
	}
	return nil, fmt.Errorf("perfil %q não encontrado", name)
}

// UpsertProfile inserts or replaces a profile by case-insensitive name.
func UpsertProfile(path string, profile Profile) error {
	profile = profile.Sanitized()
	if err := profile.Validate(); err != nil {
		return err
	}
	profiles, err := LoadProfiles(path)
	if err != nil {
		return err
	}
	replaced := false
	for i := range profiles {
		if strings.EqualFold(profiles[i].Name, profile.Name) {
			profiles[i] = profile
			replaced = true
			break
		}
	}
	if !replaced {
		profiles = append(profiles, profile)
	}
	return SaveProfiles(path, profiles)
}

// DeleteProfile removes a profile by case-insensitive name.
func DeleteProfile(path, name string) error {
	profiles, err := LoadProfiles(path)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(name)
	out := make([]Profile, 0, len(profiles))
	removed := false
	for _, p := range profiles {
		if strings.EqualFold(p.Name, target) {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return fmt.Errorf("perfil %q não encontrado", name)
	}
	return SaveProfiles(path, out)
}
