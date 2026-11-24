package fetcher

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/core"
)

type recordingRepositoryRunner struct {
	scripts []string
	err     error
}

func (r *recordingRepositoryRunner) ExecScript(_ context.Context, script string) (string, error) {
	r.scripts = append(r.scripts, script)
	return "", r.err
}

type policyResolver struct {
	untrusted bool
}

func (r *policyResolver) ListDependencies(context.Context, string, bool) ([]string, error) {
	return nil, nil
}

func (r *policyResolver) DownloadCommands([]string) [][]string { return nil }

func (r *policyResolver) SetAllowUntrusted(value bool) { r.untrusted = value }

func TestAPTRepositoryContent(t *testing.T) {
	repo := core.PackageRepository{
		Name: "Interno", Family: core.RepositoryFamilyAPT,
		URL: "https://repo.interno/apt", KeyFile: "/k",
		Suite: "bookworm", Components: []string{"main", "contrib"},
	}
	source := aptSourceContent(repo, "/usr/share/keyrings/opf-1-interno.gpg")
	want := "deb [signed-by=/usr/share/keyrings/opf-1-interno.gpg] https://repo.interno/apt bookworm main contrib\n"
	if source != want {
		t.Fatalf("source = %q, esperava %q", source, want)
	}

	repo.InsecureSkipGPGCheck = true
	repo.KeyFile = ""
	if got := aptSourceContent(repo, ""); !strings.HasPrefix(got, "deb [trusted=yes] https://repo.interno/apt bookworm") {
		t.Fatalf("trusted source = %q", got)
	}

	flat := repo
	flat.Flat = true
	flat.Suite = ""
	flat.Components = nil
	if got := aptSourceContent(flat, "/keyring"); !strings.HasSuffix(got, " ./\n") {
		t.Fatalf("flat source = %q", got)
	}
}

func TestAPTConfContent(t *testing.T) {
	repo := core.PackageRepository{URL: "https://repo.interno/apt"}
	conf := aptConfContent(repo, "repo.interno", "/etc/apt/ca.pem")
	if !strings.Contains(conf, `Acquire::https::repo.interno::CaInfo "/etc/apt/ca.pem";`) {
		t.Fatalf("conf = %q", conf)
	}

	repo.InsecureSkipTLSVerify = true
	conf = aptConfContent(repo, "repo.interno", "")
	if !strings.Contains(conf, `Acquire::https::repo.interno::Verify-Peer "false";`) {
		t.Fatalf("conf insecure = %q", conf)
	}
}

func TestDNFRepoContent(t *testing.T) {
	repo := core.PackageRepository{
		Name: "RPM", Family: core.RepositoryFamilyRPM, URL: "https://repo.interno/el9/$basearch/",
	}
	content := dnfRepoContent(repo, "7-rpm", 7, "/etc/pki/rpm-gpg/RPM-GPG-KEY-opf-7-rpm", "/etc/pki/opf/7-rpm-ca.pem")
	for _, want := range []string{
		"[opf-7-rpm]",
		"baseurl=https://repo.interno/el9/$basearch/",
		"gpgcheck=1",
		"gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-opf-7-rpm",
		"repo_gpgcheck=0",
		"sslverify=1",
		"sslcacert=/etc/pki/opf/7-rpm-ca.pem",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("conteúdo sem %q: %s", want, content)
		}
	}

	content = dnfRepoContent(repo, "7-rpm", 7, "/key", "")
	if strings.Contains(content, "sslcacert") {
		t.Fatalf("CA não informada não deveria gerar sslcacert: %s", content)
	}

	repo.InsecureSkipGPGCheck = true
	repo.InsecureSkipTLSVerify = true
	content = dnfRepoContent(repo, "7-rpm", 7, "", "")
	if !strings.Contains(content, "gpgcheck=0") || !strings.Contains(content, "sslverify=0") {
		t.Fatalf("conteúdo inseguro = %s", content)
	}
}

func TestBuildAPTRepositoryScript(t *testing.T) {
	key := []byte("KEYDATA")
	repo := core.PackageRepository{
		Name: "Interno", Family: core.RepositoryFamilyAPT,
		URL: "https://repo.interno/apt", KeyFile: "/k", Suite: "bookworm",
		Components: []string{"main"},
	}
	script, err := buildRepositoryScript(repo, 7, key, nil)
	if err != nil {
		t.Fatalf("buildRepositoryScript: %v", err)
	}
	for _, want := range []string{
		"/etc/apt/sources.list.d/opf-7-interno.list",
		"/usr/share/keyrings/opf-7-interno.gpg",
		base64.StdEncoding.EncodeToString([]byte(aptSourceContent(repo, "/usr/share/keyrings/opf-7-interno.gpg"))),
		base64.StdEncoding.EncodeToString(key),
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script sem %q:\n%s", want, script)
		}
	}
}

func TestBuildAPTRepositoryScriptArmoredKey(t *testing.T) {
	key := []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nxx\n-----END PGP PUBLIC KEY BLOCK-----")
	repo := core.PackageRepository{
		Name: "Interno", Family: core.RepositoryFamilyAPT,
		URL: "https://repo.interno/apt", KeyFile: "/k", Suite: "bookworm",
	}
	script, err := buildRepositoryScript(repo, 7, key, nil)
	if err != nil {
		t.Fatalf("buildRepositoryScript: %v", err)
	}
	if !strings.Contains(script, "gpg --dearmor") {
		t.Fatalf("script não converte chave armored:\n%s", script)
	}
}

func TestBuildAPKRepositoryScript(t *testing.T) {
	key := []byte("-----BEGIN PUBLIC KEY-----\nxx\n-----END PUBLIC KEY-----")
	repo := core.PackageRepository{
		Name: "APK", Family: core.RepositoryFamilyAPK, URL: "https://repo.interno/alpine", KeyFile: "/k",
	}
	script, err := buildRepositoryScript(repo, 7, key, []byte("CA"))
	if err != nil {
		t.Fatalf("buildRepositoryScript: %v", err)
	}
	for _, want := range []string{
		"/etc/apk/keys/opf-7-apk.rsa.pub",
		"grep -qxF 'https://repo.interno/alpine' /etc/apk/repositories",
		"ca-certificates.crt",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script sem %q:\n%s", want, script)
		}
	}

	repo.InsecureSkipGPGCheck = true
	script, err = buildRepositoryScript(repo, 7, nil, nil)
	if err != nil {
		t.Fatalf("buildRepositoryScript insecure: %v", err)
	}
	if strings.Contains(script, ".rsa.pub") {
		t.Fatalf("script inseguro não deveria instalar chave:\n%s", script)
	}
}

func TestConfigureRepositories(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("KEYDATA"), 0o600); err != nil {
		t.Fatalf("escrever chave: %v", err)
	}
	runner := &recordingRepositoryRunner{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repos := []core.PackageRepository{{
		Name: "Interno", URL: "https://repo.interno/apt", KeyFile: keyFile,
		Suite: "bookworm", Components: []string{"main"},
	}}
	if err := configureRepositories(context.Background(), runner, core.RepositoryFamilyAPT, 7, repos, logger); err != nil {
		t.Fatalf("configureRepositories: %v", err)
	}
	if len(runner.scripts) != 1 {
		t.Fatalf("scripts = %d", len(runner.scripts))
	}
	if !strings.Contains(runner.scripts[0], "/etc/apt/sources.list.d/opf-7-interno.list") {
		t.Fatalf("script inesperado:\n%s", runner.scripts[0])
	}
}

func TestConfigureRepositoriesRejectsFamilyMismatch(t *testing.T) {
	runner := &recordingRepositoryRunner{}
	repos := []core.PackageRepository{{
		Name: "RPM", Family: core.RepositoryFamilyRPM, URL: "https://repo.interno/rpm", KeyFile: "/k",
	}}
	err := configureRepositories(context.Background(), runner, core.RepositoryFamilyAPT, 7, repos, nil)
	if err == nil || !strings.Contains(err.Error(), "não corresponde ao alvo") {
		t.Fatalf("erro = %v", err)
	}
	if len(runner.scripts) != 0 {
		t.Fatalf("nenhum script deveria rodar: %v", runner.scripts)
	}
}

func TestDownloadRepositoryFileTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth := r.URL.Query().Get("auth"); wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("KEYDATA"))
	}))
	defer server.Close()

	ctx := context.Background()
	repo := core.PackageRepository{KeyURL: server.URL + "/key"}
	if _, err := downloadRepositoryFile(ctx, repo, nil, core.ProxyConfig{}); err == nil {
		t.Fatal("CA autoassinada deveria falhar por padrão")
	}
	insecureRepo := core.PackageRepository{KeyURL: server.URL + "/key", InsecureSkipTLSVerify: true}
	data, err := downloadRepositoryFile(ctx, insecureRepo, nil, core.ProxyConfig{})
	if err != nil || string(data) != "KEYDATA" {
		t.Fatalf("insecure = %q, %v", data, err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	data, err = downloadRepositoryFile(ctx, repo, caPEM, core.ProxyConfig{})
	if err != nil || string(data) != "KEYDATA" {
		t.Fatalf("CA customizada = %q, %v", data, err)
	}
	bearerRepo := core.PackageRepository{
		KeyURL:    server.URL + "/key?auth=Bearer+tok-abc",
		AuthType:  core.RepositoryAuthBearer,
		AuthToken: "tok-abc",
	}
	data, err = downloadRepositoryFile(ctx, bearerRepo, caPEM, core.ProxyConfig{})
	if err != nil || string(data) != "KEYDATA" {
		t.Fatalf("Bearer auth = %q, %v", data, err)
	}
}

func TestDownloadRepositoryFileLimitsAndUserinfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", repositoryMaterialLimit+1)))
	}))
	defer server.Close()
	if _, err := downloadRepositoryFile(context.Background(), core.PackageRepository{KeyURL: server.URL + "/key"}, nil, core.ProxyConfig{}); err == nil || !strings.Contains(err.Error(), "maior que") {
		t.Fatalf("erro = %v", err)
	}

	withUser := strings.Replace(server.URL, "http://", "http://user:pass@", 1) + "/key"
	if _, err := downloadRepositoryFile(context.Background(), core.PackageRepository{KeyURL: withUser}, nil, core.ProxyConfig{}); err == nil || !strings.Contains(err.Error(), "usuário/senha") {
		t.Fatalf("erro = %v", err)
	}
}

func TestReadRepositoryFileRejectsDirectory(t *testing.T) {
	if _, err := readRepositoryFile(t.TempDir()); err == nil {
		t.Fatal("diretório deveria ser rejeitado")
	}
}

func TestCleanupRepositories(t *testing.T) {
	runner := &recordingRepositoryRunner{}
	repos := []core.PackageRepository{{Name: "Interno", URL: "https://repo", KeyFile: "/k", Suite: "main"}}
	if err := cleanupRepositories(context.Background(), runner, core.RepositoryFamilyAPT, 7, repos); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(runner.scripts) != 1 || !strings.Contains(runner.scripts[0], "opf-7-") {
		t.Fatalf("scripts = %v", runner.scripts)
	}

	runner = &recordingRepositoryRunner{}
	if err := cleanupRepositories(context.Background(), runner, core.RepositoryFamilyRPM, 7, repos); err != nil {
		t.Fatalf("cleanup rpm: %v", err)
	}
	if len(runner.scripts) != 0 {
		t.Fatalf("RPM não deveria ter cleanup: %v", runner.scripts)
	}
}

func TestApplyRepositoryPolicy(t *testing.T) {
	resolver := &policyResolver{}
	applyRepositoryPolicy(resolver, artifactAPK, []core.PackageRepository{{Family: core.RepositoryFamilyAPK, InsecureSkipGPGCheck: true}})
	if !resolver.untrusted {
		t.Fatal("APK inseguro deveria habilitar --allow-untrusted")
	}

	resolver = &policyResolver{}
	applyRepositoryPolicy(resolver, artifactAPK, []core.PackageRepository{{Family: core.RepositoryFamilyAPK}})
	if resolver.untrusted {
		t.Fatal("APK seguro não deveria habilitar --allow-untrusted")
	}

	resolver = &policyResolver{}
	applyRepositoryPolicy(resolver, artifactDeb, []core.PackageRepository{{InsecureSkipGPGCheck: true}})
	if resolver.untrusted {
		t.Fatal("APT não deveria habilitar política APK")
	}
}

func TestRepositoryFamilyForKind(t *testing.T) {
	tests := map[artifactKind]string{
		artifactDeb: core.RepositoryFamilyAPT,
		artifactRPM: core.RepositoryFamilyRPM,
		artifactAPK: core.RepositoryFamilyAPK,
	}
	for kind, want := range tests {
		if got := repositoryFamilyForKind(kind); got != want {
			t.Fatalf("repositoryFamilyForKind(%q) = %q", kind, got)
		}
	}
}

func TestRepositoryRunnerErrorIsWrapped(t *testing.T) {
	runner := &recordingRepositoryRunner{err: errors.New("falha simulada")}
	repos := []core.PackageRepository{{
		Name: "Interno", URL: "https://repo.interno/apt", Suite: "main", InsecureSkipGPGCheck: true,
	}}
	err := configureRepositories(context.Background(), runner, core.RepositoryFamilyAPT, 7, repos, nil)
	if err == nil || !strings.Contains(err.Error(), "Interno") || !strings.Contains(err.Error(), "falha simulada") {
		t.Fatalf("erro = %v", err)
	}
}

func TestSanitizeRepositoryURL(t *testing.T) {
	got := sanitizeRepositoryURL("https://user:pass@repo.interno/path?token=abc#frag")
	if got != "https://repo.interno/path" {
		t.Fatalf("sanitize = %q", got)
	}
}

func TestBuildPrivateRepoAuthScripts(t *testing.T) {
	t.Run("apt basic auth", func(t *testing.T) {
		repo := core.PackageRepository{
			Name: "Corp", Family: core.RepositoryFamilyAPT,
			URL: "https://repo.corp.local/apt", Suite: "jammy",
			InsecureSkipGPGCheck: true,
			AuthType:             core.RepositoryAuthBasic,
			AuthUsername:         "svc-ci",
			AuthPassword:         "s3cr3t-pass",
		}
		script, err := buildRepositoryScript(repo, 9, nil, nil)
		if err != nil {
			t.Fatalf("buildRepositoryScript: %v", err)
		}
		if !strings.Contains(script, "/etc/apt/auth.conf.d/opf-9-corp.conf") || !strings.Contains(script, "chmod 0600") {
			t.Fatalf("script sem auth.conf.d 0600:\n%s", script)
		}
	})
	t.Run("apt bearer token", func(t *testing.T) {
		repo := core.PackageRepository{
			Name: "CorpBearer", Family: core.RepositoryFamilyAPT,
			URL: "https://repo.corp.local/apt", Suite: "jammy",
			InsecureSkipGPGCheck: true,
			AuthType:             core.RepositoryAuthBearer,
			AuthToken:            "tok-xyz-123",
		}
		script, err := buildRepositoryScript(repo, 9, nil, nil)
		if err != nil {
			t.Fatalf("buildRepositoryScript: %v", err)
		}
		if !strings.Contains(script, "/etc/apt/apt.conf.d/99opf-9-corpbearer") || !strings.Contains(script, "chmod 0600") {
			t.Fatalf("script sem apt.conf.d 0600:\n%s", script)
		}
	})
	t.Run("rpm basic and bearer", func(t *testing.T) {
		basic := core.PackageRepository{
			Name: "RpmBasic", Family: core.RepositoryFamilyRPM,
			URL: "https://repo.corp.local/rpm", InsecureSkipGPGCheck: true,
			AuthType: core.RepositoryAuthBasic, AuthUsername: "u", AuthPassword: "p",
		}
		content := dnfRepoContent(basic, "9-rpmbasic", 9, "", "")
		if !strings.Contains(content, "username=u") || !strings.Contains(content, "password=p") {
			t.Fatalf("dnfRepoContent basic = %s", content)
		}
		bearer := core.PackageRepository{
			Name: "RpmBearer", Family: core.RepositoryFamilyRPM,
			URL: "https://repo.corp.local/rpm", InsecureSkipGPGCheck: true,
			AuthType: core.RepositoryAuthBearer, AuthToken: "tok-rpm",
		}
		content = dnfRepoContent(bearer, "9-rpmbearer", 9, "", "")
		if !strings.Contains(content, "set_http_headers=Authorization: Bearer tok-rpm") {
			t.Fatalf("dnfRepoContent bearer = %s", content)
		}
	})
	t.Run("redactRepoSecrets", func(t *testing.T) {
		repo := core.PackageRepository{
			AuthType: core.RepositoryAuthBasic, AuthUsername: "u", AuthPassword: "my-password", AuthToken: "my-token",
		}
		redacted := redactRepoSecrets(errors.New("failed with my-password and my-token"), repo)
		if redacted == nil || strings.Contains(redacted.Error(), "my-password") || strings.Contains(redacted.Error(), "my-token") {
			t.Fatalf("segredo não redigido: %v", redacted)
		}
	})
}
