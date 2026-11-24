package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"opf/internal/core"
)

// Settings are non-secret global preferences. Passwords and private key
// material must never be added here.
type Settings struct {
	FetchMethod      string `json:"fetch_method,omitempty"`
	ContainerHost    string `json:"container_host,omitempty"`
	TempDir          string `json:"temp_dir,omitempty"`
	DatabasePath     string `json:"database_path,omitempty"`
	StageTimeoutSecs int    `json:"stage_timeout_seconds,omitempty"`
	DockerImage      string `json:"docker_image,omitempty"`
	WSLDistro        string `json:"wsl_distro,omitempty"`
	HTTPProxy        string `json:"http_proxy,omitempty"`
	HTTPSProxy       string `json:"https_proxy,omitempty"`
	NoProxy          string `json:"no_proxy,omitempty"`
	CACertFile       string `json:"ca_cert_file,omitempty"`
	// Language is the interface language: "auto" follows the operating system,
	// otherwise "en", "pt-BR" or "es".
	Language        string `json:"language,omitempty"`
	IncludeOptional bool   `json:"include_optional"`
	ForceReinstall  bool   `json:"force_reinstall"`
	SyslogEnabled   bool   `json:"syslog_enabled"`
	SyslogHost      string `json:"syslog_host,omitempty"`
	SyslogPort      int    `json:"syslog_port,omitempty"`
	SyslogProto     string `json:"syslog_proto,omitempty"`
	SyslogTag       string `json:"syslog_tag,omitempty"`
}

func Default() Settings {
	return Settings{
		FetchMethod:      "docker",
		StageTimeoutSecs: int((10 * time.Minute) / time.Second),
		Language:         "auto",
		SyslogProto:      "udp",
		SyslogTag:        "opf",
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolver diretório de configuração: %w", err)
	}
	return filepath.Join(dir, "opf", "config.json"), nil
}

// DefaultDatabasePath is the per-user SQLite path used by the GUI when no
// database_path is configured. Windows uses %LocalAppData%\OPF\data, macOS
// uses ~/Library/Application Support/OPF/data and Linux uses
// $XDG_DATA_HOME/OPF/data (or ~/.local/share/OPF/data). The installer creates
// the same location.
func DefaultDatabasePath() (string, error) {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
		if base == "" {
			dir, err := os.UserCacheDir()
			if err != nil {
				return "", fmt.Errorf("resolver diretório de dados: %w", err)
			}
			base = dir
		}
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolver diretório de dados: %w", err)
		}
		base = filepath.Join(home, "Library", "Application Support")
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolver diretório de dados: %w", err)
			}
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, "OPF", "data", "opf.db"), nil
}

// DefaultOutputDir is the GUI's writable default for downloaded artifacts.
func DefaultOutputDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolver diretório do usuário: %w", err)
	}
	return filepath.Join(home, "OPF"), nil
}

func Load(path string) (Settings, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Settings{}, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("ler configurações %q: %w", path, err)
	}
	settings := Default()
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("interpretar configurações %q: %w", path, err)
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func Save(path string, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("criar diretório de configuração: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar configurações: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("criar configuração temporária: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("ajustar permissões da configuração: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("escrever configuração: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sincronizar configuração: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fechar configuração: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publicar configuração: %w", err)
	}
	return nil
}

func (s Settings) StageTimeout() time.Duration {
	if s.StageTimeoutSecs <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(s.StageTimeoutSecs) * time.Second
}

func (s Settings) ProxyConfig() core.ProxyConfig {
	return core.ProxyConfig{
		HTTPProxy:  s.HTTPProxy,
		HTTPSProxy: s.HTTPSProxy,
		NoProxy:    s.NoProxy,
		CACertFile: s.CACertFile,
	}
}

func (s Settings) Validate() error {
	switch s.FetchMethod {
	case "", "docker", "podman", "wsl", "auto":
	default:
		return fmt.Errorf("método de fetch inválido: %q", s.FetchMethod)
	}
	if err := s.ProxyConfig().Validate(true); err != nil {
		return err
	}
	if s.StageTimeoutSecs < 0 {
		return errors.New("timeout de etapa não pode ser negativo")
	}
	if s.SyslogPort < 0 || s.SyslogPort > 65535 {
		return fmt.Errorf("porta syslog inválida: %d", s.SyslogPort)
	}
	switch s.SyslogProto {
	case "", "udp", "tcp":
	default:
		return fmt.Errorf("protocolo syslog inválido: %q", s.SyslogProto)
	}
	switch s.Language {
	case "", "auto", "en", "pt-BR", "es":
	default:
		return fmt.Errorf("idioma inválido: %q (use auto, en, pt-BR ou es)", s.Language)
	}
	return nil
}
