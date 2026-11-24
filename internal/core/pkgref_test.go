package core

import "testing"

func TestParsePackageSpecValidAndPinned(t *testing.T) {
	tests := []struct {
		family  string
		raw     string
		name    string
		arch    string
		version string
	}{
		{"apt", "vim", "vim", "", ""},
		{"apt", "libssl3:amd64", "libssl3", "amd64", ""},
		{"apt", "nginx=1.18.0-6ubuntu14.4", "nginx", "", "1.18.0-6ubuntu14.4"},
		{"apt", "vim:amd64=2:8.2.3995-1ubuntu2.16", "vim", "amd64", "2:8.2.3995-1ubuntu2.16"},
		{"rpm", "vim-enhanced", "vim-enhanced", "", ""},
		{"rpm", "nginx=1.20.1-14.el9", "nginx", "", "1.20.1-14.el9"},
		{"apk", "curl", "curl", "", ""},
		{"apk", "curl=8.9.1-r1", "curl", "", "8.9.1-r1"},
	}
	for _, tc := range tests {
		got, err := ParsePackageSpec(tc.family, tc.raw)
		if err != nil {
			t.Fatalf("ParsePackageSpec(%s, %q): %v", tc.family, tc.raw, err)
		}
		if got.Name != tc.name || got.Arch != tc.arch || got.Version != tc.version {
			t.Fatalf("ParsePackageSpec(%s, %q) = %+v", tc.family, tc.raw, got)
		}
		if base := PackageBaseName(tc.raw); base != tc.name {
			t.Fatalf("PackageBaseName(%q) = %q, want %q", tc.raw, base, tc.name)
		}
	}
}

func TestParsePackageSpecRejectsInjections(t *testing.T) {
	invalid := []string{
		"", "vim=", "vim=1.0;id", "vim=$(id)", "vim=`id`",
		"vim=1.0 --force", "vim=../1.0", "vim=1.0\nid", "-v=1", "Vim=1.0",
	}
	for _, raw := range invalid {
		if _, err := ParsePackageSpec("apt", raw); err == nil {
			t.Errorf("APT accepted invalid spec %q", raw)
		}
	}
	for _, raw := range []string{"", "vim=", "vim=1.0;id", "vim=$(id)", "vim=1.0 --force"} {
		if _, err := ParsePackageSpec("rpm", raw); err == nil {
			t.Errorf("RPM accepted invalid spec %q", raw)
		}
		if _, err := ParsePackageSpec("apk", raw); err == nil {
			t.Errorf("APK accepted invalid spec %q", raw)
		}
	}
}

func TestProxyConfigValidationAndSanitization(t *testing.T) {
	cfg := ProxyConfig{
		HTTPProxy:  "http://user:supersecret@proxy.corp:8080/path?x=1",
		HTTPSProxy: "https://proxy.corp:8443",
		NoProxy:    "localhost,127.0.0.1,.corp.local",
		CACertFile: "/etc/ssl/corp-ca.pem",
	}
	if err := cfg.Validate(false); err != nil {
		t.Fatalf("Validate(false) rejeitou proxy válido: %v", err)
	}
	if err := cfg.Validate(true); err == nil {
		t.Fatal("Validate(true) deveria rejeitar credenciais em URL persistida")
	}
	sanitized := cfg.Sanitized()
	if sanitized.HTTPProxy != "http://proxy.corp:8080/path" {
		t.Fatalf("Sanitized HTTPProxy = %q", sanitized.HTTPProxy)
	}
	env := cfg.EnvMap()
	if env["HTTP_PROXY"] != cfg.HTTPProxy || env["https_proxy"] != cfg.HTTPSProxy || env["NO_PROXY"] != cfg.NoProxy {
		t.Fatalf("EnvMap inesperado: %+v", env)
	}
}
