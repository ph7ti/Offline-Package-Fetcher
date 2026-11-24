package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/i18n"
)

// TestMain pins the CLI tests to Brazilian Portuguese, the source language of
// the interface, so assertions on user-visible output stay stable regardless
// of the host locale.
func TestMain(m *testing.M) {
	i18n.Set(i18n.PTBR)
	os.Exit(m.Run())
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("leitura falhou") }

func TestParseJumpHostSpecs(t *testing.T) {
	base := core.SSHConfig{
		Host: "destino", Port: 22, User: "opusuario",
		KeyPath: "/tmp/key", KnownHosts: "/tmp/known_hosts", InsecureSkipHostKey: true,
	}

	tests := []struct {
		spec string
		want core.SSHConfig
	}{
		{
			spec: "jump",
			want: core.SSHConfig{Host: "jump", Port: 22, User: "opusuario"},
		},
		{
			spec: "salto@jump:2200",
			want: core.SSHConfig{Host: "jump", Port: 2200, User: "salto"},
		},
		{
			spec: "[::1]:2222",
			want: core.SSHConfig{Host: "::1", Port: 2222, User: "opusuario"},
		},
		{
			spec: "outro@[2001:db8::1]:22",
			want: core.SSHConfig{Host: "2001:db8::1", Port: 22, User: "outro"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := parseJumpHost(tt.spec, base, "password", "segredo", "", "")
			if err != nil {
				t.Fatalf("parseJumpHost: %v", err)
			}
			if got.Host != tt.want.Host || got.Port != tt.want.Port || got.User != tt.want.User {
				t.Fatalf("jump = %+v, esperava host=%s port=%d user=%s",
					got, tt.want.Host, tt.want.Port, tt.want.User)
			}
			if got.Password != "segredo" || got.KeyPath != "" {
				t.Fatalf("modo senha aplicado incorretamente: %+v", got)
			}
			if got.KnownHosts != base.KnownHosts || !got.InsecureSkipHostKey {
				t.Fatalf("known_hosts/host key não herdados: %+v", got)
			}
			if len(got.JumpHosts) != 0 {
				t.Fatalf("jump não pode ter jumps aninhados: %+v", got)
			}
		})
	}
}

func TestParseJumpHostAuthModes(t *testing.T) {
	base := core.SSHConfig{
		Host: "destino", Port: 22, User: "opusuario",
		KeyPath: "/dest/key", Password: "dest-pass", KnownHosts: "/tmp/known_hosts",
	}

	tests := []struct {
		name     string
		auth     string
		password string
		key      string
		known    string
		wantKey  string
		wantPass string
		wantKH   string
	}{
		{"senha", "password", "pw", "/ignored", "", "", "pw", base.KnownHosts},
		{"chave", "key", "ignored", "/jump/key", "", "/jump/key", "", base.KnownHosts},
		{"chave e senha", "key+password", "pw", "/jump/key", "/jump/kh", "/jump/key", "pw", "/jump/kh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJumpHost("salto@jump:2200", base, tt.auth, tt.password, tt.key, tt.known)
			if err != nil {
				t.Fatalf("parseJumpHost: %v", err)
			}
			if got.KeyPath != tt.wantKey || got.Password != tt.wantPass {
				t.Fatalf("credenciais = key %q pass %q", got.KeyPath, got.Password)
			}
			if got.KnownHosts != tt.wantKH {
				t.Fatalf("known_hosts = %q", got.KnownHosts)
			}
		})
	}
}

func TestParseJumpHostInvalid(t *testing.T) {
	base := core.SSHConfig{Host: "destino", Port: 22, User: "opf", KeyPath: "/tmp/key"}
	for _, spec := range []string{"", "@jump", "user@", "jump:abc", "jump:0", "jump:70000", "2001:db8::1"} {
		t.Run(spec, func(t *testing.T) {
			if _, err := parseJumpHost(spec, base, "password", "pw", "", ""); err == nil {
				t.Fatalf("esperava erro para %q", spec)
			}
		})
	}
}

func TestParseJumpHostMissingCredentials(t *testing.T) {
	base := core.SSHConfig{Host: "destino", Port: 22, User: "opf"}
	tests := []struct {
		name     string
		auth     string
		password string
		key      string
		want     string
		secret   string
	}{
		{"senha ausente", "password", "", "", "Ponte 1 exige senha", ""},
		{"chave ausente", "key", "", "", "Ponte 1 exige chave privada", ""},
		{"chave e senha sem chave", "key+password", "segredo", "", "Ponte 1 exige chave privada", "segredo"},
		{"modo inválido", "otp", "segredo", "", "modo de autenticação inválido", "segredo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseJumpHost("jump", base, tt.auth, tt.password, tt.key, "")
			if err == nil {
				t.Fatal("esperava erro")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("erro = %q, esperava %q", err, tt.want)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Fatalf("erro vazou segredo: %q", err)
			}
		})
	}
}

func TestBuildJumpConfigsKeepsOrderAndDefaults(t *testing.T) {
	base := core.SSHConfig{Host: "destino", Port: 2222, User: "opf", KnownHosts: "/dest/kh"}
	specs := []string{"a@bridge1", "b@bridge2:2200"}
	auths := []string{"key", "password"}
	passwords := []string{"", "pw2"}
	keys := []string{"/bridge1/key", ""}

	jumps, err := buildJumpConfigs(base, specs, auths, passwords, keys, nil)
	if err != nil {
		t.Fatalf("buildJumpConfigs: %v", err)
	}
	if len(jumps) != 2 {
		t.Fatalf("jumps = %d", len(jumps))
	}
	if jumps[0].Host != "bridge1" || jumps[0].Port != 22 || jumps[0].User != "a" || jumps[0].KeyPath != "/bridge1/key" || jumps[0].Password != "" {
		t.Fatalf("jump 0 = %+v", jumps[0])
	}
	if jumps[1].Host != "bridge2" || jumps[1].Port != 2200 || jumps[1].User != "b" || jumps[1].KeyPath != "" || jumps[1].Password != "pw2" {
		t.Fatalf("jump 1 = %+v", jumps[1])
	}
	for _, jump := range jumps {
		if jump.KnownHosts != base.KnownHosts {
			t.Fatalf("known_hosts não herdado: %+v", jump)
		}
	}
}

func TestValidateJumpFlagCounts(t *testing.T) {
	if err := validateJumpFlagCounts(nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("sem pontes: %v", err)
	}
	if err := validateJumpFlagCounts(nil, []string{"password"}, nil, nil, nil); err == nil {
		t.Fatal("esperava erro para --ssh-jump-auth sem --ssh-jump")
	}
	if err := validateJumpFlagCounts([]string{"a"}, nil, nil, nil, nil); err == nil {
		t.Fatal("esperava erro para --ssh-jump sem --ssh-jump-auth")
	}
	if err := validateJumpFlagCounts([]string{"a", "b"}, []string{"password"}, nil, nil, nil); err == nil {
		t.Fatal("esperava erro para quantidade divergente de autenticação")
	}
	if err := validateJumpFlagCounts([]string{"a", "b"}, []string{"password", "key"}, []string{"pw"}, nil, nil); err == nil {
		t.Fatal("esperava erro para quantidade divergente de senhas")
	}
	if err := validateJumpFlagCounts(
		[]string{"a", "b"},
		[]string{"key", "password"},
		[]string{"", "pw"},
		[]string{"/k", ""},
		[]string{"", ""},
	); err != nil {
		t.Fatalf("placeholders vazios deveriam ser aceitos: %v", err)
	}
}

func TestDecodeJumpPasswordsEnv(t *testing.T) {
	values, err := decodeJumpPasswordsEnv(`["a","b"]`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(values) != 2 || values[1] != "b" {
		t.Fatalf("values = %v", values)
	}
	if values, err := decodeJumpPasswordsEnv("  "); err != nil || values != nil {
		t.Fatalf("vazio = %v, %v", values, err)
	}
	if _, err := decodeJumpPasswordsEnv("nao-json"); err == nil {
		t.Fatal("esperava erro para JSON inválido")
	}
}

func TestConfirmInstallation(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		assumeYes bool
		want      bool
	}{
		{"yes flag", "", true, true},
		{"y", "y\n", false, true},
		{"YES", "YES\n", false, true},
		{"yes sem newline", "yes", false, true},
		{"n", "n\n", false, false},
		{"empty line", "\n", false, false},
		{"eof", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			got, err := confirmInstallation(strings.NewReader(tt.input), &output, 3, "opf@host", tt.assumeYes)
			if err != nil {
				t.Fatalf("confirmInstallation: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resposta = %v, esperava %v", got, tt.want)
			}
			if !tt.assumeYes && !strings.Contains(output.String(), "Instalar 3 pacotes em opf@host") {
				t.Fatalf("prompt inesperado: %q", output.String())
			}
		})
	}

	var output strings.Builder
	if _, err := confirmInstallation(errorReader{}, &output, 1, "opf@host", false); err == nil {
		t.Fatal("esperava erro de leitura")
	}
}

func TestMergeJumpPasswords(t *testing.T) {
	if _, err := mergeJumpPasswords([]string{"segredo-flag"}, []string{"segredo-env"}); err == nil {
		t.Fatal("esperava conflito entre flag e variável de ambiente")
	} else if !strings.Contains(err.Error(), "--ssh-jump-password") || strings.Contains(err.Error(), "segredo") {
		t.Fatalf("mensagem inesperada: %v", err)
	}

	got, err := mergeJumpPasswords([]string{"flag"}, nil)
	if err != nil || len(got) != 1 || got[0] != "flag" {
		t.Fatalf("flag = %v, %v", got, err)
	}
	got, err = mergeJumpPasswords(nil, []string{"env"})
	if err != nil || len(got) != 1 || got[0] != "env" {
		t.Fatalf("env = %v, %v", got, err)
	}
}

func TestResolveRepositoriesSimple(t *testing.T) {
	setFlags := map[string]bool{"repo-url": true, "repo-apt-suite": true}
	repos, err := resolveRepositories(repositoryFlags{
		url: "https://repo.interno/apt", name: "Interno", aptSuite: "bookworm",
		aptComponents: "main,contrib", insecureGPG: true,
	}, core.RepositoryFamilyAPT, setFlags)
	if err != nil {
		t.Fatalf("resolveRepositories: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %d", len(repos))
	}
	repo := repos[0]
	if repo.Family != core.RepositoryFamilyAPT || repo.Suite != "bookworm" {
		t.Fatalf("repo = %+v", repo)
	}
	if strings.Join(repo.Components, ",") != "main,contrib" {
		t.Fatalf("components = %v", repo.Components)
	}
	if !repo.InsecureSkipGPGCheck {
		t.Fatal("opt-in inseguro não propagado")
	}

	rpm, err := resolveRepositories(repositoryFlags{
		url: "https://repo.interno/rpm", insecureGPG: true,
	}, core.RepositoryFamilyRPM, map[string]bool{"repo-url": true})
	if err != nil {
		t.Fatalf("RPM: %v", err)
	}
	if rpm[0].Family != core.RepositoryFamilyRPM || rpm[0].Suite != "" || len(rpm[0].Components) != 0 {
		t.Fatalf("repo RPM = %+v", rpm[0])
	}
}

func TestResolveRepositoriesConflicts(t *testing.T) {
	tests := []struct {
		name    string
		flags   repositoryFlags
		set     map[string]bool
		family  string
		wantErr string
	}{
		{
			name: "config e flag", flags: repositoryFlags{configPath: "x.json", url: "https://repo"},
			set: map[string]bool{"repo-url": true}, family: core.RepositoryFamilyAPT, wantErr: "mutuamente exclusivo",
		},
		{
			name: "sem URL", flags: repositoryFlags{key: "/k"},
			set: map[string]bool{"repo-key": true}, family: core.RepositoryFamilyAPT, wantErr: "--repo-url é obrigatório",
		},
		{
			name: "apt flags em rpm", flags: repositoryFlags{url: "https://repo", aptSuite: "main"},
			set: map[string]bool{"repo-url": true, "repo-apt-suite": true}, family: core.RepositoryFamilyRPM, wantErr: "apenas a repositórios APT",
		},
		{
			name: "flat com components", flags: repositoryFlags{url: "https://repo", aptFlat: true, aptComponents: "main"},
			set: map[string]bool{"repo-url": true, "repo-apt-flat": true, "repo-apt-components": true}, family: core.RepositoryFamilyAPT, wantErr: "não se aplica",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveRepositories(tt.flags, tt.family, tt.set)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("erro = %v, esperava %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRepositoryConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	content := `[{"name":"Interno","family":"apt","url":"https://repo.interno/apt","key_file":"key.asc","ca_file":"ca.pem","suite":"bookworm"}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escrever JSON: %v", err)
	}
	repos, err := loadRepositoryConfigFile(path)
	if err != nil {
		t.Fatalf("loadRepositoryConfigFile: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %d", len(repos))
	}
	if repos[0].KeyFile != filepath.Join(dir, "key.asc") || repos[0].CAFile != filepath.Join(dir, "ca.pem") {
		t.Fatalf("caminhos relativos não resolvidos: %+v", repos[0])
	}
}

func TestLoadRepositoryConfigFileRejectsUnknownAndTrailing(t *testing.T) {
	dir := t.TempDir()
	tests := map[string]string{
		"unknown":  `[{"name":"x","url":"https://repo","key_file":"/k","suite":"main","extra":1}]`,
		"trailing": `[] []`,
		"invalid":  `{`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("escrever: %v", err)
			}
			if _, err := loadRepositoryConfigFile(path); err == nil {
				t.Fatal("esperava erro")
			}
		})
	}
}

func TestResolveRepositoriesFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	content := `[{"name":"Interno","family":"rpm","url":"https://repo.interno/el9/$basearch","key_file":"/tmp/key"}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escrever JSON: %v", err)
	}
	repos, err := resolveRepositories(repositoryFlags{configPath: path}, core.RepositoryFamilyRPM, map[string]bool{"repo-config": true})
	if err != nil {
		t.Fatalf("resolveRepositories: %v", err)
	}
	if len(repos) != 1 || repos[0].Family != core.RepositoryFamilyRPM {
		t.Fatalf("repos = %+v", repos)
	}
}

func TestApplyLanguageFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		want    i18n.Language
		wantErr bool
	}{
		{name: "sem flag segue OPF_LANG", env: "pt-BR", want: i18n.PTBR},
		{name: "flag en vence OPF_LANG", args: []string{"-lang", "en"}, env: "pt-BR", want: i18n.EN},
		{name: "flag es inline", args: []string{"-lang=es"}, env: "pt-BR", want: i18n.ES},
		{name: "flag auto segue OPF_LANG", args: []string{"--lang", "auto"}, env: "es", want: i18n.ES},
		{name: "flag dupla em português", args: []string{"--lang", "pt-BR"}, env: "en", want: i18n.PTBR},
		{name: "última ocorrência vence", args: []string{"-lang", "en", "-lang", "es"}, env: "pt-BR", want: i18n.ES},
		{name: "flag antes de outras flags", args: []string{"-pkg", "curl", "-lang", "en"}, env: "pt-BR", want: i18n.EN},
		{name: "valor inválido cai no fallback", args: []string{"-lang", "xx"}, env: "pt-BR", want: i18n.PTBR, wantErr: true},
		{name: "flag sem valor", args: []string{"-lang"}, env: "pt-BR", want: i18n.PTBR, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPF_LANG", tt.env)
			t.Cleanup(func() { i18n.Set(i18n.PTBR) })
			err := applyLanguageFlag(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("esperava erro de --lang")
				}
				if !strings.Contains(err.Error(), "--lang") {
					t.Fatalf("erro = %q, esperava mencionar --lang", err)
				}
			} else if err != nil {
				t.Fatalf("applyLanguageFlag: %v", err)
			}
			if got := i18n.Current(); got != tt.want {
				t.Fatalf("idioma = %q, esperava %q", got, tt.want)
			}
		})
	}
}

func TestApplyLanguageFlagInvalidUsesFallbackLanguage(t *testing.T) {
	t.Setenv("OPF_LANG", "es")
	t.Cleanup(func() { i18n.Set(i18n.PTBR) })
	err := applyLanguageFlag([]string{"-lang", "xx"})
	if err == nil || !strings.Contains(err.Error(), "--lang no válido") {
		t.Fatalf("erro = %v, esperava mensagem em espanhol", err)
	}
}

func TestRunVersionFlag(t *testing.T) {
	if err := run([]string{"--version"}); err != nil {
		t.Fatalf("--version retornou erro: %v", err)
	}
}

func TestRunInitDBFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", "opf.db")
	if err := run([]string{"--init-db", "--db", dbPath}); err != nil {
		t.Fatalf("--init-db retornou erro: %v", err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("banco não criado: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("banco criado com 0 bytes; schema SQLite não foi inicializado")
	}
}

func TestLoadPackageFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pkgs.txt")
	content := "# pacotes principais\nvim\ncurl, git # inline comment\n\n  htop  \n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escrever arquivo: %v", err)
	}
	pkgs, err := loadPackageFile(path)
	if err != nil {
		t.Fatalf("loadPackageFile: %v", err)
	}
	want := "vim,curl,git,htop"
	if got := strings.Join(pkgs, ","); got != want {
		t.Fatalf("pacotes = %q, esperava %q", got, want)
	}
	if _, err := loadPackageFile(dir); err == nil {
		t.Fatal("esperava erro ao passar diretório para --pkg-file")
	}
}

func TestResumeFlagRejectsNonPositive(t *testing.T) {
	err := run([]string{"--resume", "0", "--pkg", "vim", "--target", "ubuntu:22.04", "--arch", "amd64"})
	if err == nil || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("esperava erro de --resume inválido, obteve %v", err)
	}
}

func TestProfileListAndDeleteFlags(t *testing.T) {
	profilesPath := filepath.Join(t.TempDir(), "profiles.json")
	if err := config.UpsertProfile(profilesPath, config.Profile{
		Name: "prod-ub22", TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64",
	}); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}
	if err := run([]string{"--profiles-file", profilesPath, "--list-profiles"}); err != nil {
		t.Fatalf("--list-profiles: %v", err)
	}
	if err := run([]string{"--profiles-file", profilesPath, "--delete-profile", "prod-ub22"}); err != nil {
		t.Fatalf("--delete-profile: %v", err)
	}
	if _, err := config.GetProfile(profilesPath, "prod-ub22"); err == nil {
		t.Fatal("perfil deveria ter sido removido")
	}
}

func TestBuildProxyConfigInherit(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.corp:8080")
	t.Setenv("HTTPS_PROXY", "http://secure.corp:8443")
	t.Setenv("NO_PROXY", "localhost,.corp.local")
	got := buildProxyConfig("", "", "", "/tmp/ca.pem", true)
	if got.HTTPProxy != "http://proxy.corp:8080" || got.HTTPSProxy != "http://secure.corp:8443" || got.NoProxy != "localhost,.corp.local" || got.CACertFile != "/tmp/ca.pem" {
		t.Fatalf("proxy herdado incorreto: %+v", got)
	}
	explicit := buildProxyConfig("http://override:3128", "", "", "", true)
	if explicit.HTTPProxy != "http://override:3128" {
		t.Fatalf("flag explícita deveria vencer env: %+v", explicit)
	}
}

func TestApplyRepoAuthEnvAndMergeJumpPassphrases(t *testing.T) {
	repos := []core.PackageRepository{{Name: "Corp Repo", URL: "https://repo.corp/apt"}}
	updated, err := applyRepoAuthEnv(repos, `{"corp-repo":{"type":"bearer","token":"tok-123"}}`)
	if err != nil {
		t.Fatalf("applyRepoAuthEnv: %v", err)
	}
	if updated[0].AuthType != core.RepositoryAuthBearer || updated[0].AuthToken != "tok-123" {
		t.Fatalf("auth não aplicado: %+v", updated[0])
	}
	if _, err := applyRepoAuthEnv(repos, "not-json"); err == nil {
		t.Fatal("esperava erro para OPF_REPO_AUTH inválido")
	}
	if _, err := mergeJumpPassphrases([]string{"a"}, []string{"b"}); err == nil {
		t.Fatal("esperava erro ao informar flag e env para passphrase")
	}
	got, err := mergeJumpPassphrases(nil, []string{"p1"})
	if err != nil || len(got) != 1 || got[0] != "p1" {
		t.Fatalf("mergeJumpPassphrases = %v, %v", got, err)
	}
}
