package fetcher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"opf/internal/core"
)

const (
	repositoryMaterialLimit = 1 << 20
	repositoryDownloadLimit = 15 * time.Second
)

type repositoryRunner interface {
	ExecScript(ctx context.Context, script string) (string, error)
}

type dockerRepositoryRunner struct {
	runtime containerRuntime
}

func (r dockerRepositoryRunner) ExecScript(ctx context.Context, script string) (string, error) {
	return r.runtime.Exec(ctx, []string{"sh", "-c", script})
}

// configureRepositories validates the list and writes the family-specific
// repository configuration inside the isolated environment. It never writes
// repository configuration on the remote destination.
func configureRepositories(ctx context.Context, runner repositoryRunner, family string, sessionID int64, repos []core.PackageRepository, logger *slog.Logger) error {
	return configureRepositoriesWithProxy(ctx, runner, family, sessionID, repos, core.ProxyConfig{}, logger)
}

func configureRepositoriesWithProxy(ctx context.Context, runner repositoryRunner, family string, sessionID int64, repos []core.PackageRepository, proxy core.ProxyConfig, logger *slog.Logger) error {
	if len(repos) == 0 {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	normalized, err := core.NormalizePackageRepositories(repos, family)
	if err != nil {
		return err
	}
	for _, repo := range normalized {
		key, ca, err := loadRepositoryMaterial(ctx, repo, proxy)
		if err != nil {
			return fmt.Errorf("carregar material do repositório %q: %w", repo.Name, redactRepoSecrets(err, repo))
		}
		script, err := buildRepositoryScript(repo, sessionID, key, ca)
		if err != nil {
			return fmt.Errorf("montar repositório %q: %w", repo.Name, redactRepoSecrets(err, repo))
		}
		if repo.InsecureSkipGPGCheck {
			logger.Warn("repositório adicional com verificação de GPG desabilitada",
				"repositorio", repo.Name, "familia", family)
		}
		if repo.InsecureSkipTLSVerify {
			logger.Warn("repositório adicional com verificação de TLS desabilitada",
				"repositorio", repo.Name, "familia", family)
		}
		if strings.HasPrefix(strings.ToLower(repo.URL), "http://") {
			logger.Warn("repositório adicional usa HTTP sem TLS; a assinatura GPG continua obrigatória por padrão",
				"repositorio", repo.Name, "familia", family)
		}
		if repo.AuthType != "" {
			logger.Info("autenticação efêmera configurada para repositório adicional",
				"repositorio", repo.Name, "familia", family, "auth_type", repo.AuthType)
		}
		logger.Info("configurando repositório adicional",
			"repositorio", repo.Name, "familia", family, "url", sanitizeRepositoryURL(repo.URL))
		if _, err := runner.ExecScript(ctx, script); err != nil {
			return fmt.Errorf("configurar repositório %q: %w", repo.Name, redactRepoSecrets(err, repo))
		}
	}
	return nil
}

// cleanupRepositories removes session-scoped repository files from a
// persistent environment. Docker containers are ephemeral, so this is used by
// the WSL fetcher.
func cleanupRepositories(ctx context.Context, runner repositoryRunner, family string, sessionID int64, repos []core.PackageRepository) error {
	if runner == nil || len(repos) == 0 {
		return nil
	}
	if family != core.RepositoryFamilyAPT {
		return nil
	}
	prefix := fmt.Sprintf("opf-%d-", sessionID)
	script := fmt.Sprintf(
		"rm -f /etc/apt/sources.list.d/%s*.list /etc/apt/apt.conf.d/99%s* /etc/apt/auth.conf.d/%s*.conf /usr/share/keyrings/%s*.gpg /etc/apt/%s*-ca.pem",
		prefix, prefix, prefix, prefix, prefix,
	)
	if _, err := runner.ExecScript(ctx, script); err != nil {
		return fmt.Errorf("remover repositórios adicionais do ambiente: %w", err)
	}
	return nil
}

func repositoryFamilyForKind(kind artifactKind) string {
	switch kind {
	case artifactRPM:
		return core.RepositoryFamilyRPM
	case artifactAPK:
		return core.RepositoryFamilyAPK
	default:
		return core.RepositoryFamilyAPT
	}
}

func applyRepositoryPolicy(resolver core.DependencyResolver, kind artifactKind, repos []core.PackageRepository) {
	if resolver == nil || kind != artifactAPK {
		return
	}
	for _, repo := range repos {
		if repo.InsecureSkipGPGCheck {
			if repo.Family != "" && repo.Family != core.RepositoryFamilyAPK {
				continue
			}
			if setter, ok := resolver.(interface{ SetAllowUntrusted(bool) }); ok {
				setter.SetAllowUntrusted(true)
			}
			return
		}
	}
}

func buildRepositoryScript(repo core.PackageRepository, sessionID int64, key, ca []byte) (string, error) {
	switch repo.Family {
	case core.RepositoryFamilyAPT:
		return buildAPTRepositoryScript(repo, sessionID, key, ca)
	case core.RepositoryFamilyRPM:
		return buildRPMRepositoryScript(repo, sessionID, key, ca)
	case core.RepositoryFamilyAPK:
		return buildAPKRepositoryScript(repo, sessionID, key, ca)
	default:
		return "", fmt.Errorf("família de repositório não suportada: %q", repo.Family)
	}
}

func buildAPTRepositoryScript(repo core.PackageRepository, sessionID int64, key, ca []byte) (string, error) {
	prefix := repositoryFilePrefix(sessionID, repo.Name)
	keyringPath := "/usr/share/keyrings/opf-" + prefix + ".gpg"
	sourcePath := "/etc/apt/sources.list.d/opf-" + prefix + ".list"
	confPath := "/etc/apt/apt.conf.d/99opf-" + prefix
	caPath := "/etc/apt/opf-" + prefix + "-ca.pem"

	script := newRepositoryScript()
	if !repo.InsecureSkipGPGCheck {
		if len(key) == 0 {
			return "", fmt.Errorf("chave do repositório ausente")
		}
		if isArmoredPGPKey(key) {
			tempPath := "/tmp/opf-" + prefix + ".key"
			script.addFile(tempPath, key, "0600")
			script.addCommand("if command -v gpg >/dev/null 2>&1; then gpg --dearmor -o " + quoteRepositoryPath(keyringPath) + " " + quoteRepositoryPath(tempPath) + "; " +
				"else DEBIAN_FRONTEND=noninteractive apt-get update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y gnupg >/dev/null && gpg --dearmor -o " + quoteRepositoryPath(keyringPath) + " " + quoteRepositoryPath(tempPath) + "; fi")
			script.addCommand("rm -f " + quoteRepositoryPath(tempPath))
			script.addCommand("chmod 0644 " + quoteRepositoryPath(keyringPath))
		} else {
			script.addFile(keyringPath, key, "0644")
		}
	}

	var source strings.Builder
	source.WriteString(aptSourceContent(repo, keyringPath))
	script.addFile(sourcePath, []byte(source.String()), "0644")

	host := repositoryURLHost(repo.URL)
	if strings.HasPrefix(strings.ToLower(repo.URL), "https://") && (len(ca) > 0 || repo.InsecureSkipTLSVerify) {
		if host == "" {
			return "", fmt.Errorf("URL HTTPS sem host")
		}
		if len(ca) > 0 {
			script.addFile(caPath, ca, "0644")
		}
	}
	confCAPath := ""
	if len(ca) > 0 && strings.HasPrefix(strings.ToLower(repo.URL), "https://") {
		confCAPath = caPath
	}
	confBody := aptConfContent(repo, host, confCAPath)
	if confBody != "" {
		mode := "0644"
		if repo.AuthType == core.RepositoryAuthBearer {
			mode = "0600"
		}
		script.addFile(confPath, []byte(confBody), mode)
	}
	if repo.AuthType == core.RepositoryAuthBasic {
		if host == "" {
			return "", fmt.Errorf("URL sem host para autenticação APT")
		}
		authPath := "/etc/apt/auth.conf.d/opf-" + prefix + ".conf"
		authBody := fmt.Sprintf("machine %s login %s password %s\n", host, repo.AuthUsername, repo.AuthPassword)
		script.addFile(authPath, []byte(authBody), "0600")
	}
	return script.String(), nil
}

func aptSourceContent(repo core.PackageRepository, keyringPath string) string {
	option := "trusted=yes"
	if !repo.InsecureSkipGPGCheck {
		option = "signed-by=" + keyringPath
	}
	var source strings.Builder
	fmt.Fprintf(&source, "deb [%s] %s", option, repo.URL)
	if repo.Flat {
		source.WriteString(" ./\n")
	} else {
		fmt.Fprintf(&source, " %s %s\n", repo.Suite, strings.Join(repo.Components, " "))
	}
	return source.String()
}

func aptConfContent(repo core.PackageRepository, host, caPath string) string {
	var conf strings.Builder
	if len(caPath) > 0 {
		fmt.Fprintf(&conf, "Acquire::https::%s::CaInfo \"%s\";\n", host, caPath)
	}
	if repo.InsecureSkipTLSVerify {
		fmt.Fprintf(&conf, "Acquire::https::%s::Verify-Peer \"false\";\n", host)
	}
	if repo.AuthType == core.RepositoryAuthBearer && host != "" {
		scheme := "https"
		if strings.HasPrefix(strings.ToLower(repo.URL), "http://") {
			scheme = "http"
		}
		fmt.Fprintf(&conf, "Acquire::%s::%s::ExtraHeader:: \"Authorization: Bearer %s\";\n", scheme, host, repo.AuthToken)
	}
	return conf.String()
}

func buildRPMRepositoryScript(repo core.PackageRepository, sessionID int64, key, ca []byte) (string, error) {
	prefix := repositoryFilePrefix(sessionID, repo.Name)
	repoPath := "/etc/yum.repos.d/opf-" + prefix + ".repo"
	keyPath := "/etc/pki/rpm-gpg/RPM-GPG-KEY-opf-" + prefix
	caPath := "/etc/pki/opf/" + prefix + "-ca.pem"

	script := newRepositoryScript()
	if !repo.InsecureSkipGPGCheck {
		if len(key) == 0 {
			return "", fmt.Errorf("chave do repositório ausente")
		}
		script.addFile(keyPath, key, "0644")
		script.addCommand("rpm --import " + quoteRepositoryPath(keyPath))
	}
	if len(ca) > 0 && !repo.InsecureSkipTLSVerify {
		script.addFile(caPath, ca, "0644")
	}
	confCAPath := ""
	if len(ca) > 0 && !repo.InsecureSkipTLSVerify {
		confCAPath = caPath
	}
	repoMode := "0644"
	if repo.AuthType != "" {
		repoMode = "0600"
	}
	script.addFile(repoPath, []byte(dnfRepoContent(repo, prefix, sessionID, keyPath, confCAPath)), repoMode)
	return script.String(), nil
}

func dnfRepoContent(repo core.PackageRepository, prefix string, sessionID int64, keyPath, caPath string) string {
	var content strings.Builder
	fmt.Fprintf(&content, "[opf-%s]\n", prefix)
	fmt.Fprintf(&content, "name=OPF %d %s\n", sessionID, repo.Name)
	fmt.Fprintf(&content, "baseurl=%s\n", repo.URL)
	content.WriteString("enabled=1\n")
	if repo.InsecureSkipGPGCheck {
		content.WriteString("gpgcheck=0\n")
	} else {
		content.WriteString("gpgcheck=1\n")
		fmt.Fprintf(&content, "gpgkey=file://%s\n", keyPath)
	}
	content.WriteString("repo_gpgcheck=0\n")
	if repo.InsecureSkipTLSVerify {
		content.WriteString("sslverify=0\n")
	} else {
		content.WriteString("sslverify=1\n")
		if len(caPath) > 0 {
			fmt.Fprintf(&content, "sslcacert=%s\n", caPath)
		}
	}
	switch repo.AuthType {
	case core.RepositoryAuthBasic:
		fmt.Fprintf(&content, "username=%s\n", repo.AuthUsername)
		fmt.Fprintf(&content, "password=%s\n", repo.AuthPassword)
	case core.RepositoryAuthBearer:
		fmt.Fprintf(&content, "set_http_headers=Authorization: Bearer %s\n", repo.AuthToken)
	}
	return content.String()
}

func buildAPKRepositoryScript(repo core.PackageRepository, sessionID int64, key, ca []byte) (string, error) {
	if repo.InsecureSkipTLSVerify {
		return "", fmt.Errorf("APK não suporta ignorar TLS por repositório")
	}
	if repo.AuthType == core.RepositoryAuthBearer {
		return "", fmt.Errorf("APK não suporta autenticação Bearer; use Basic Auth")
	}
	prefix := repositoryFilePrefix(sessionID, repo.Name)
	script := newRepositoryScript()
	if !repo.InsecureSkipGPGCheck {
		if len(key) == 0 {
			return "", fmt.Errorf("chave do repositório ausente")
		}
		script.addFile("/etc/apk/keys/opf-"+prefix+".rsa.pub", key, "0644")
	}
	if len(ca) > 0 {
		caPath := "/tmp/opf-" + prefix + "-ca.pem"
		script.addFile(caPath, ca, "0644")
		script.addCommand("mkdir -p /etc/ssl/certs && touch /etc/ssl/certs/ca-certificates.crt && cat " +
			quoteRepositoryPath(caPath) + " >> /etc/ssl/certs/ca-certificates.crt && rm -f " + quoteRepositoryPath(caPath))
	}
	script.addCommand("mkdir -p /etc/apk && touch /etc/apk/repositories")
	repoLine := repo.URL
	if repo.AuthType == core.RepositoryAuthBasic {
		parsed, err := url.Parse(repo.URL)
		if err != nil {
			return "", err
		}
		parsed.User = url.UserPassword(repo.AuthUsername, repo.AuthPassword)
		repoLine = parsed.String()
		script.addCommand("chmod 0600 /etc/apk/repositories")
	}
	urlLine := quoteRepositoryPath(repoLine)
	script.addCommand("grep -qxF " + urlLine + " /etc/apk/repositories || printf '%s\\n' " + urlLine + " >> /etc/apk/repositories")
	return script.String(), nil
}

func loadRepositoryMaterial(ctx context.Context, repo core.PackageRepository, proxy core.ProxyConfig) (key, ca []byte, err error) {
	if repo.CAFile != "" {
		ca, err = readRepositoryFile(repo.CAFile)
		if err != nil {
			return nil, nil, fmt.Errorf("ler CA %q: %w", repo.CAFile, err)
		}
	}
	switch {
	case repo.KeyFile != "":
		key, err = readRepositoryFile(repo.KeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("ler chave %q: %w", repo.KeyFile, err)
		}
	case repo.KeyURL != "":
		key, err = downloadRepositoryFile(ctx, repo, ca, proxy)
		if err != nil {
			return nil, nil, fmt.Errorf("baixar chave %q: %w", sanitizeRepositoryURL(repo.KeyURL), err)
		}
	}
	if !repo.InsecureSkipGPGCheck && len(key) == 0 {
		return nil, nil, fmt.Errorf("chave do repositório ausente")
	}
	return key, ca, nil
}

func readRepositoryFile(filename string) ([]byte, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("não é arquivo regular")
	}
	if info.Size() > repositoryMaterialLimit {
		return nil, fmt.Errorf("arquivo maior que %d bytes", repositoryMaterialLimit)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if len(data) > repositoryMaterialLimit {
		return nil, fmt.Errorf("arquivo maior que %d bytes", repositoryMaterialLimit)
	}
	return data, nil
}

func downloadRepositoryFile(ctx context.Context, repo core.PackageRepository, ca []byte, proxy core.ProxyConfig) ([]byte, error) {
	rawURL := repo.KeyURL
	insecure := repo.InsecureSkipTLSVerify
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("URL deve usar http ou https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("URL sem host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("URL não pode conter usuário/senha")
	}

	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	if proxyURL := selectProxyURL(parsed.Scheme, proxy); proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	var corpCA []byte
	if len(ca) == 0 && strings.TrimSpace(proxy.CACertFile) != "" {
		if data, caErr := readRepositoryFile(proxy.CACertFile); caErr == nil {
			corpCA = data
		}
	}
	effectiveCA := ca
	if len(effectiveCA) == 0 {
		effectiveCA = corpCA
	}
	if strings.EqualFold(parsed.Scheme, "https") && (len(effectiveCA) > 0 || insecure) {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if insecure {
			// #nosec G402 -- explicit, documented opt-in (--repo-insecure-tls); see docs/SECURITY.md
			tlsConfig.InsecureSkipVerify = true
		} else {
			pool, poolErr := x509.SystemCertPool()
			if poolErr != nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(effectiveCA) {
				return nil, fmt.Errorf("CA do repositório inválida")
			}
			tlsConfig.RootCAs = pool
		}
		transport.TLSClientConfig = tlsConfig
	}
	client := &http.Client{Timeout: repositoryDownloadLimit, Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	switch repo.AuthType {
	case core.RepositoryAuthBasic:
		request.SetBasicAuth(repo.AuthUsername, repo.AuthPassword)
	case core.RepositoryAuthBearer:
		request.Header.Set("Authorization", "Bearer "+repo.AuthToken)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("status HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, repositoryMaterialLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > repositoryMaterialLimit {
		return nil, fmt.Errorf("arquivo maior que %d bytes", repositoryMaterialLimit)
	}
	return data, nil
}

func selectProxyURL(scheme string, proxy core.ProxyConfig) *url.URL {
	raw := strings.TrimSpace(proxy.HTTPProxy)
	if strings.EqualFold(scheme, "https") && strings.TrimSpace(proxy.HTTPSProxy) != "" {
		raw = strings.TrimSpace(proxy.HTTPSProxy)
	}
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

func redactRepoSecrets(err error, repo core.PackageRepository) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	changed := false
	for _, secret := range []string{repo.AuthPassword, repo.AuthToken} {
		if secret != "" && strings.Contains(msg, secret) {
			msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
			changed = true
		}
	}
	if !changed {
		return err
	}
	return fmt.Errorf("%s", msg)
}

type repositoryScript struct {
	builder strings.Builder
	dirs    map[string]struct{}
}

func newRepositoryScript() *repositoryScript {
	script := &repositoryScript{dirs: make(map[string]struct{})}
	script.builder.WriteString("set -eu\n")
	return script
}

func (s *repositoryScript) addFile(filename string, content []byte, mode string) {
	dir := path.Dir(filename)
	if _, ok := s.dirs[dir]; !ok {
		s.dirs[dir] = struct{}{}
		fmt.Fprintf(&s.builder, "mkdir -p %s\n", quoteRepositoryPath(dir))
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	fmt.Fprintf(&s.builder, "printf '%%s' '%s' | base64 -d > %s\n", encoded, quoteRepositoryPath(filename))
	fmt.Fprintf(&s.builder, "chmod %s %s\n", mode, quoteRepositoryPath(filename))
}

func (s *repositoryScript) addCommand(command string) {
	s.builder.WriteString(command)
	s.builder.WriteString("\n")
}

func (s *repositoryScript) String() string {
	return s.builder.String()
}

func isArmoredPGPKey(data []byte) bool {
	return strings.HasPrefix(strings.TrimSpace(string(data)), "-----BEGIN PGP PUBLIC KEY BLOCK-----")
}

func repositoryFilePrefix(sessionID int64, name string) string {
	return fmt.Sprintf("%d-%s", sessionID, core.RepositorySlug(name))
}

func repositoryURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// sanitizeRepositoryURL removes credentials, query and fragment before a URL
// is written to logs or error messages.
func sanitizeRepositoryURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func quoteRepositoryPath(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
