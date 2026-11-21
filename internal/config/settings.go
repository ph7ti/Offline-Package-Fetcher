package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Settings are non-secret global preferences. Passwords and private key
// material must never be added here.
type Settings struct {
	FetchMethod      string `json:"fetch_method,omitempty"`
	TempDir          string `json:"temp_dir,omitempty"`
	DatabasePath     string `json:"database_path,omitempty"`
	StageTimeoutSecs int    `json:"stage_timeout_seconds,omitempty"`
	DockerImage      string `json:"docker_image,omitempty"`
	WSLDistro        string `json:"wsl_distro,omitempty"`
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

// DefaultDatabasePath is the GUI's writable default for the SQLite history.
// It deliberately does not depend on the process working directory: the GUI
// may be launched from a read-only folder (for example a temporary extraction
// directory), where creating opf.db fails.
func DefaultDatabasePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolver diretório de dados: %w", err)
	}
	return filepath.Join(dir, "opf", "opf.db"), nil
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

func (s Settings) Validate() error {
	switch s.FetchMethod {
	case "", "docker", "wsl", "auto":
	default:
		return fmt.Errorf("método de fetch inválido: %q", s.FetchMethod)
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
