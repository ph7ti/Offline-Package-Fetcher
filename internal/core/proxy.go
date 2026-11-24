package core

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"opf/internal/i18n"
)

// ProxyConfig configures optional HTTP/HTTPS proxy and custom corporate CA
// injection inside the isolated download environment. Credentials embedded in
// proxy URLs are kept only in memory and stripped by Sanitized() before any
// persistence or logging.
type ProxyConfig struct {
	HTTPProxy  string `json:"http_proxy,omitempty"`
	HTTPSProxy string `json:"https_proxy,omitempty"`
	NoProxy    string `json:"no_proxy,omitempty"`
	CACertFile string `json:"ca_cert_file,omitempty"`
}

// Empty reports whether no proxy or custom CA is configured.
func (p ProxyConfig) Empty() bool {
	return strings.TrimSpace(p.HTTPProxy) == "" &&
		strings.TrimSpace(p.HTTPSProxy) == "" &&
		strings.TrimSpace(p.NoProxy) == "" &&
		strings.TrimSpace(p.CACertFile) == ""
}

// Validate validates proxy URLs and CA path syntax (without doing disk I/O).
// When forbidCredentials is true (used for persisted config.json/profiles.json),
// proxy URLs containing userinfo (user:pass@) are rejected.
func (p ProxyConfig) Validate(forbidCredentials bool) error {
	for _, item := range []struct {
		name string
		raw  string
	}{
		{"HTTP_PROXY", strings.TrimSpace(p.HTTPProxy)},
		{"HTTPS_PROXY", strings.TrimSpace(p.HTTPSProxy)},
	} {
		if item.raw == "" {
			continue
		}
		if err := validateProxyURL(item.name, item.raw, forbidCredentials); err != nil {
			return err
		}
	}
	if strings.ContainsAny(p.NoProxy, "\r\n\x00") {
		return errors.New(i18n.T("NO_PROXY contém caracteres inválidos"))
	}
	if strings.ContainsAny(p.CACertFile, "\r\n\x00") {
		return errors.New(i18n.T("caminho da CA corporativa contém caracteres inválidos"))
	}
	return nil
}

// Sanitized returns a copy of ProxyConfig with any credentials stripped from
// HTTP/HTTPS proxy URLs, safe for persistence in SQLite/config or logs.
func (p ProxyConfig) Sanitized() ProxyConfig {
	return ProxyConfig{
		HTTPProxy:  SanitizeProxyURL(p.HTTPProxy),
		HTTPSProxy: SanitizeProxyURL(p.HTTPSProxy),
		NoProxy:    strings.TrimSpace(p.NoProxy),
		CACertFile: strings.TrimSpace(p.CACertFile),
	}
}

// EnvMap builds the environment variables injected into the isolated container.
func (p ProxyConfig) EnvMap() map[string]string {
	env := make(map[string]string)
	if v := strings.TrimSpace(p.HTTPProxy); v != "" {
		env["HTTP_PROXY"] = v
		env["http_proxy"] = v
	}
	if v := strings.TrimSpace(p.HTTPSProxy); v != "" {
		env["HTTPS_PROXY"] = v
		env["https_proxy"] = v
	}
	if v := strings.TrimSpace(p.NoProxy); v != "" {
		env["NO_PROXY"] = v
		env["no_proxy"] = v
	}
	return env
}

// SanitizeProxyURL removes userinfo (user:password@), query and fragment from a
// proxy URL so secrets never appear in logs, SQLite or reports.
func SanitizeProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid-proxy-url>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func validateProxyURL(label, raw string, forbidCredentials bool) error {
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return fmt.Errorf(i18n.T("%s contém caracteres inválidos"), label)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf(i18n.T("%s inválido: %w"), label, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf(i18n.T("%s deve usar http ou https"), label)
	}
	if parsed.Host == "" {
		return fmt.Errorf(i18n.T("%s sem host"), label)
	}
	if forbidCredentials && parsed.User != nil {
		return fmt.Errorf(i18n.T("%s persistido não pode conter usuário/senha"), label)
	}
	return nil
}
