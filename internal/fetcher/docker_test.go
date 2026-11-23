package fetcher

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/core"
)

type fakeRuntime struct {
	id           string
	execFn       func(cmd []string) (string, error)
	files        map[string]string
	copied       []string
	terminated   bool
	terminateCnt int
	terminateErr error
}

func (r *fakeRuntime) ID() string { return r.id }

func (r *fakeRuntime) Exec(_ context.Context, cmd []string) (string, error) {
	if r.execFn == nil {
		return "", nil
	}
	return r.execFn(cmd)
}

func (r *fakeRuntime) CopyFromContainer(_ context.Context, path string) (io.ReadCloser, error) {
	r.copied = append(r.copied, path)
	content, ok := r.files[path]
	if !ok {
		return nil, fmt.Errorf("arquivo não encontrado no contêiner: %s", path)
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func (r *fakeRuntime) Terminate(context.Context) error {
	r.terminated = true
	r.terminateCnt++
	return r.terminateErr
}

type fakeResolver struct {
	deps      []string
	listErr   error
	commands  [][]string
	calls     int
	roots     []string
	untrusted bool
}

func (r *fakeResolver) ListDependencies(_ context.Context, pkg string, _ bool) ([]string, error) {
	r.calls++
	r.roots = append(r.roots, pkg)
	return r.deps, r.listErr
}

func (r *fakeResolver) DownloadCommands([]string) [][]string {
	return r.commands
}

func (r *fakeResolver) SetAllowUntrusted(value bool) {
	r.untrusted = value
}

func newTestFetcher(t *testing.T, rt *fakeRuntime, res *fakeResolver) *DockerFetcher {
	t.Helper()
	f := NewDockerFetcher(
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			if exec != core.CommandExecutor(rt) {
				t.Errorf("executor recebido pela factory não é o runtime do contêiner")
			}
			if arch != "amd64" {
				t.Errorf("arch recebida pela factory = %q", arch)
			}
			if workDir != "/tmp/opf-42" {
				t.Errorf("workdir recebido pela factory = %q", workDir)
			}
			return res
		}))
	f.factory = func(_ context.Context, cfg runConfig) (containerRuntime, error) {
		if cfg.Image == "" || cfg.Platform == "" || cfg.ContainerName == "" || cfg.WorkDir == "" {
			t.Errorf("configuração incompleta do contêiner: %+v", cfg)
		}
		return rt, nil
	}
	if err := f.BindSession(42); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	t.Cleanup(func() {
		if err := f.Cleanup(context.Background()); err != nil {
			t.Errorf("cleanup do teste: %v", err)
		}
	})
	return f
}

func unameForTest(arch string) string {
	switch canonicalArch(arch) {
	case "amd64":
		return "x86_64\n"
	case "arm64":
		return "aarch64\n"
	case "armhf":
		return "armv7l\n"
	case "i386":
		return "i686\n"
	case "ppc64el":
		return "ppc64le\n"
	default:
		return arch + "\n"
	}
}

func archRuntime(arch string) *fakeRuntime {
	return &fakeRuntime{
		id: "fake-container",
		execFn: func(cmd []string) (string, error) {
			if strings.Join(cmd, " ") == "uname -m" {
				return unameForTest(arch), nil
			}
			return "", nil
		},
	}
}

func TestDockerFetcherPrepareCleanup(t *testing.T) {
	rt := archRuntime("amd64")
	res := &fakeResolver{}
	f := newTestFetcher(t, rt, res)
	ctx := context.Background()

	if err := f.Prepare(ctx, "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if f.workDir != "/tmp/opf-42" {
		t.Fatalf("workdir = %q", f.workDir)
	}
	if f.ContainerID() != "fake-container" {
		t.Fatalf("container id = %q", f.ContainerID())
	}
	hostDir := f.HostDir()
	if hostDir == "" {
		t.Fatal("hostDir vazio após Prepare")
	}
	if _, err := os.Stat(hostDir); err != nil {
		t.Fatalf("hostDir não existe: %v", err)
	}

	if err := f.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !rt.terminated {
		t.Fatal("contêiner não foi terminado")
	}
	if _, err := os.Stat(hostDir); !os.IsNotExist(err) {
		t.Fatalf("hostDir deveria ter sido removido, stat err=%v", err)
	}
	if err := f.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup idempotente: %v", err)
	}
	if rt.terminateCnt != 1 {
		t.Fatalf("Terminate chamado %d vezes", rt.terminateCnt)
	}
}

func TestDockerFetcherPrepareArchMismatch(t *testing.T) {
	rt := archRuntime("arm64")
	f := newTestFetcher(t, rt, &fakeResolver{})

	err := f.Prepare(context.Background(), "ubuntu", "22.04", "amd64")
	if err == nil || !strings.Contains(err.Error(), "difere") {
		t.Fatalf("esperava erro de arquitetura: %v", err)
	}
	if !rt.terminated {
		t.Fatal("contêiner deveria ser removido após falha no Prepare")
	}
	if f.HostDir() != "" {
		t.Fatalf("hostDir não foi limpo: %q", f.HostDir())
	}
}

func TestDockerFetcherPrepareValidations(t *testing.T) {
	ctx := context.Background()

	unbound := NewDockerFetcher(WithResolverFactory(func(core.CommandExecutor, string, string) core.DependencyResolver {
		return &fakeResolver{}
	}))
	if err := unbound.Prepare(ctx, "ubuntu", "22.04", "amd64"); err == nil {
		t.Fatal("esperava erro sem sessão vinculada")
	}

	f := newTestFetcher(t, archRuntime("amd64"), &fakeResolver{})
	if err := f.Prepare(ctx, "archlinux", "2024.01", "amd64"); err == nil || !strings.Contains(err.Error(), "não suportada") {
		t.Fatalf("esperava erro de distro não suportada: %v", err)
	}

	f2 := newTestFetcher(t, archRuntime("amd64"), &fakeResolver{})
	if err := f2.Prepare(ctx, "ubuntu", "22.04", "mips"); err == nil {
		t.Fatal("esperava erro de arquitetura não suportada")
	}

	f3 := newTestFetcher(t, archRuntime("amd64"), &fakeResolver{})
	if err := f3.Prepare(ctx, "ubuntu", "22/04", "amd64"); err == nil {
		t.Fatal("esperava erro de versão inválida")
	}
}

func debHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestDockerFetcherDownload(t *testing.T) {
	vimContent := "conteudo-vim"
	commonContent := "conteudo-vim-common"
	libcContent := "conteudo-libc6"

	files := map[string]string{
		"/tmp/opf-42/vim_9.0_amd64.deb":        vimContent,
		"/tmp/opf-42/vim-common_9.0_amd64.deb": commonContent,
		"/tmp/opf-42/libc6_2.35_amd64.deb":     libcContent,
	}
	inventory := strings.Join([]string{
		"vim_9.0_amd64.deb\tvim\t9.0\tamd64\t" + debHash(vimContent),
		"vim-common_9.0_amd64.deb\tvim-common\t9.0\tamd64\t" + debHash(commonContent),
		"libc6_2.35_amd64.deb\tlibc6\t2.35\tamd64\t" + debHash(libcContent),
	}, "\n")

	rt := archRuntime("amd64")
	rt.files = files
	rt.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return "x86_64\n", nil
		}
		if cmd[0] == "sh" && len(cmd) == 3 && cmd[1] == "-c" {
			return inventory, nil
		}
		return "", nil
	}
	res := &fakeResolver{
		deps:     []string{"libc6", "vim", "vim-common"},
		commands: [][]string{{"apt-get", "download", "vim", "libc6", "vim-common"}},
	}
	f := newTestFetcher(t, rt, res)
	ctx := context.Background()
	if err := f.Prepare(ctx, "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	artifacts, err := f.Download(ctx, "vim", false)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artefatos = %d", len(artifacts))
	}
	if artifacts[0].Name != "libc6" || artifacts[1].Name != "vim" || artifacts[2].Name != "vim-common" {
		t.Fatalf("ordem dos artefatos: %+v", artifacts)
	}
	if artifacts[0].IsDependency != true || artifacts[1].IsDependency != false || artifacts[2].IsDependency != true {
		t.Fatalf("flags de dependência incorretas: %+v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Version == "" || artifact.Arch != "amd64" || artifact.SHA256 == "" {
			t.Fatalf("metadados incompletos: %+v", artifact)
		}
		data, err := os.ReadFile(artifact.LocalPath)
		if err != nil {
			t.Fatalf("ler artefato %q: %v", artifact.LocalPath, err)
		}
		if debHash(string(data)) != artifact.SHA256 {
			t.Fatalf("hash do artefato %q divergente", artifact.Name)
		}
	}
	if len(rt.copied) != 3 {
		t.Fatalf("cópias = %d", len(rt.copied))
	}
	for _, path := range rt.copied {
		if !strings.HasPrefix(path, "/tmp/opf-42/") {
			t.Errorf("cópia fora do workdir isolado: %s", path)
		}
	}
}

func TestDockerFetcherDownloadPackages(t *testing.T) {
	vim := "vim"
	curl := "curl"
	libc := "libc6"
	rt := archRuntime("amd64")
	rt.files = map[string]string{
		"/tmp/opf-42/vim_9.0_amd64.deb":    vim,
		"/tmp/opf-42/curl_8.0_amd64.deb":   curl,
		"/tmp/opf-42/libc6_2.35_amd64.deb": libc,
	}
	inventory := strings.Join([]string{
		"vim_9.0_amd64.deb\tvim\t9.0\tamd64\t" + debHash(vim),
		"curl_8.0_amd64.deb\tcurl\t8.0\tamd64\t" + debHash(curl),
		"libc6_2.35_amd64.deb\tlibc6\t2.35\tamd64\t" + debHash(libc),
	}, "\n")
	rt.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return "x86_64\n", nil
		}
		if cmd[0] == "sh" {
			return inventory, nil
		}
		return "", nil
	}
	res := &fakeResolver{
		deps:     []string{"libc6", "vim", "curl"},
		commands: [][]string{{"apt-get", "download", "vim", "curl", "libc6"}},
	}
	f := newTestFetcher(t, rt, res)
	ctx := context.Background()
	if err := f.Prepare(ctx, "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	artifacts, err := f.DownloadPackages(ctx, []string{"vim", "curl"}, false)
	if err != nil {
		t.Fatalf("DownloadPackages: %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artefatos = %d", len(artifacts))
	}
	if strings.Join(res.roots, ",") != "vim,curl" {
		t.Fatalf("roots = %v", res.roots)
	}
	flags := make(map[string]bool)
	for _, artifact := range artifacts {
		flags[artifact.Name] = artifact.IsDependency
	}
	if flags["vim"] || flags["curl"] {
		t.Fatalf("roots marcados como dependência: %+v", flags)
	}
	if !flags["libc6"] {
		t.Fatalf("libc6 deveria ser dependência: %+v", flags)
	}
}

func TestDockerFetcherDownloadMissingArtifact(t *testing.T) {
	content := "vim"
	rt := archRuntime("amd64")
	rt.files = map[string]string{
		"/tmp/opf-42/vim_9.0_amd64.deb": content,
	}
	rt.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return "x86_64\n", nil
		}
		if cmd[0] == "sh" {
			return "vim_9.0_amd64.deb\tvim\t9.0\tamd64\t" + debHash(content), nil
		}
		return "", nil
	}
	res := &fakeResolver{
		deps:     []string{"vim", "vim-runtime"},
		commands: [][]string{{"apt-get", "download", "vim", "vim-runtime"}},
	}
	f := newTestFetcher(t, rt, res)
	ctx := context.Background()
	if err := f.Prepare(ctx, "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err := f.Download(ctx, "vim", false)
	if err == nil || !strings.Contains(err.Error(), "sem artefato") {
		t.Fatalf("esperava erro de artefato faltante: %v", err)
	}
	if len(rt.copied) != 0 {
		t.Fatalf("nada deveria ser copiado antes da validação de completude")
	}
}

func TestDockerFetcherDownloadExecError(t *testing.T) {
	rt := archRuntime("amd64")
	rt.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return "x86_64\n", nil
		}
		return "", errors.New("falha no apt-get")
	}
	res := &fakeResolver{
		deps:     []string{"vim"},
		commands: [][]string{{"apt-get", "download", "vim"}},
	}
	f := newTestFetcher(t, rt, res)
	ctx := context.Background()
	if err := f.Prepare(ctx, "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err := f.Download(ctx, "vim", false)
	if err == nil || !strings.Contains(err.Error(), "baixar pacotes") {
		t.Fatalf("esperava erro de download: %v", err)
	}
}

func TestDockerFetcherDownloadNotPrepared(t *testing.T) {
	f := NewDockerFetcher()
	if _, err := f.Download(context.Background(), "vim", false); err == nil {
		t.Fatal("esperava erro sem ambiente preparado")
	}
}

func TestParseDebInventoryInvalid(t *testing.T) {
	hash := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		output  string
		wantErr string
	}{
		{"vazio", "", "vazio"},
		{"campos", "a.deb\tvim\n", "inválida"},
		{"sem deb", "vim.rpm\tvim\t1\tamd64\t" + hash + "\n", "inválido"},
		{"caminho", "../vim.deb\tvim\t1\tamd64\t" + hash + "\n", "inválido"},
		{"hash", "vim.deb\tvim\t1\tamd64\tabc\n", "SHA-256"},
		{"metadados", "vim.deb\t\t1\tamd64\t" + hash + "\n", "incompletos"},
		{"duplicado", "vim.deb\tvim\t1\tamd64\t" + hash + "\nvim.deb\tvim\t1\tamd64\t" + hash + "\n", "duplicado"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDebInventory(tt.output)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("esperava erro contendo %q, obteve %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseRPMInventory(t *testing.T) {
	hash := strings.Repeat("a", 64)
	output := "vim-enhanced-7.4.629-8.el7_9.x86_64.rpm\tvim-enhanced\t7.4.629-8.el7_9\tx86_64\t" + hash + "\n"
	infos, err := parseRPMInventory(output)
	if err != nil {
		t.Fatalf("parseRPMInventory: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "vim-enhanced" || infos[0].Arch != "x86_64" {
		t.Fatalf("inventário RPM incorreto: %+v", infos)
	}
	if _, err := parseRPMInventory("vim-enhanced.deb\tvim\t1\tx86_64\t" + hash + "\n"); err == nil {
		t.Fatal("parseRPMInventory aceitou .deb")
	}
	if _, err := parseDebInventory(output); err == nil {
		t.Fatal("parseDebInventory aceitou .rpm")
	}
}

func TestParseAPKInventory(t *testing.T) {
	hash := strings.Repeat("a", 64)
	output := "vim-9.1.0707-r0.apk\tvim\t9.1.0707-r0\tx86_64\t" + hash + "\n"
	infos, err := parseAPKInventory(output)
	if err != nil {
		t.Fatalf("parseAPKInventory: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "vim" || infos[0].Arch != "x86_64" {
		t.Fatalf("inventário APK incorreto: %+v", infos)
	}
	if _, err := parseAPKInventory("vim.deb\tvim\t1\tx86_64\t" + hash + "\n"); err == nil {
		t.Fatal("parseAPKInventory aceitou .deb")
	}
	if _, err := parseDebInventory(output); err == nil {
		t.Fatal("parseDebInventory aceitou .apk")
	}
}

func TestArtifactKindForOS(t *testing.T) {
	deb := []string{"ubuntu", "debian"}
	for _, osName := range deb {
		kind, err := artifactKindForOS(osName)
		if err != nil || kind != artifactDeb {
			t.Errorf("artifactKindForOS(%q) = %q, %v", osName, kind, err)
		}
	}
	rpm := []string{"centos", "rhel", "rockylinux", "almalinux", "fedora"}
	for _, osName := range rpm {
		kind, err := artifactKindForOS(osName)
		if err != nil || kind != artifactRPM {
			t.Errorf("artifactKindForOS(%q) = %q, %v", osName, kind, err)
		}
	}
	kind, err := artifactKindForOS("alpine")
	if err != nil || kind != artifactAPK {
		t.Errorf("artifactKindForOS(alpine) = %q, %v", kind, err)
	}
	if _, err := artifactKindForOS("archlinux"); err == nil {
		t.Fatal("esperava erro para distro não suportada")
	}
}

func TestCanonicalArch(t *testing.T) {
	tests := map[string]string{
		"amd64":   "amd64",
		"x86_64":  "amd64",
		"arm64":   "arm64",
		"aarch64": "arm64",
		"armv7l":  "armhf",
		"i686":    "i386",
		"ppc64le": "ppc64el",
		"s390x":   "s390x",
	}
	for input, want := range tests {
		if got := canonicalArch(input); got != want {
			t.Errorf("canonicalArch(%q) = %q, esperava %q", input, got, want)
		}
	}
}

func TestDockerPlatform(t *testing.T) {
	tests := map[string]string{
		"amd64":   "linux/amd64",
		"x86_64":  "linux/amd64",
		"arm64":   "linux/arm64",
		"aarch64": "linux/arm64",
		"armhf":   "linux/arm/v7",
		"armv7l":  "linux/arm/v7",
		"i386":    "linux/386",
		"i686":    "linux/386",
		"ppc64el": "linux/ppc64le",
		"ppc64le": "linux/ppc64le",
		"s390x":   "linux/s390x",
	}
	for arch, want := range tests {
		got, err := dockerPlatform(arch)
		if err != nil {
			t.Errorf("dockerPlatform(%q): %v", arch, err)
			continue
		}
		if got != want {
			t.Errorf("dockerPlatform(%q) = %q, esperava %q", arch, got, want)
		}
	}
	if _, err := dockerPlatform("mips"); err == nil {
		t.Fatal("esperava erro para arquitetura desconhecida")
	}
}

func TestDockerFetcherBindSession(t *testing.T) {
	f := NewDockerFetcher()
	if err := f.BindSession(0); err == nil {
		t.Fatal("esperava erro para id zero")
	}
	if err := f.BindSession(1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := f.BindSession(2); err == nil {
		t.Fatal("esperava erro ao trocar a sessão")
	}
	if err := f.BindSession(1); err != nil {
		t.Fatalf("BindSession idempotente: %v", err)
	}
}

func TestSupportsEPEL(t *testing.T) {
	cases := map[string]bool{
		"rockylinux": true,
		"almalinux":  true,
		"centos":     true,
		"rhel":       true,
		"fedora":     false,
		"ubuntu":     false,
	}
	for target, want := range cases {
		fetcher := &DockerFetcher{targetOS: target}
		if got := fetcher.supportsEPEL(); got != want {
			t.Fatalf("supportsEPEL(%q) = %v, esperava %v", target, got, want)
		}
	}
}

func TestEPELReleaseURL(t *testing.T) {
	for _, version := range []string{"9", "9.4", "8.10", "7.9.2009"} {
		url, ok := epelReleaseURL(version)
		if !ok || !strings.Contains(url, "epel-release-latest-"+strings.SplitN(version, ".", 2)[0]+".noarch.rpm") {
			t.Fatalf("epelReleaseURL(%q) = %q, %v", version, url, ok)
		}
	}
	if _, ok := epelReleaseURL("10"); ok {
		t.Fatal("versão sem EPEL não deveria retornar URL")
	}
}

func TestEnableEPELFallback(t *testing.T) {
	var scripts []string
	runtime := &fakeRuntime{execFn: func(cmd []string) (string, error) {
		script := strings.Join(cmd, " ")
		scripts = append(scripts, script)
		if strings.Contains(script, "epel-release") && !strings.Contains(script, "https://") {
			return "", errors.New("epel-release indisponível nos repositórios base")
		}
		return "", nil
	}}
	fetcher := &DockerFetcher{runtime: runtime, targetOS: "rockylinux", targetVersion: "9.4"}
	if err := fetcher.enableEPEL(context.Background()); err != nil {
		t.Fatalf("enableEPEL: %v", err)
	}
	joined := strings.Join(scripts, "\n")
	if !strings.Contains(joined, "epel-release-latest-9.noarch.rpm") {
		t.Fatalf("fallback não usado: %s", joined)
	}
	if !strings.Contains(joined, "config-manager --set-enabled crb") {
		t.Fatalf("CRB não habilitado: %s", joined)
	}
	if !strings.Contains(joined, "rpm --import") {
		t.Fatalf("chave EPEL não importada: %s", joined)
	}
}

func TestIsMissingPackageError(t *testing.T) {
	if !isMissingPackageError(errors.New("Error: No package mtr-tiny available.")) {
		t.Fatal("erro de pacote ausente não reconhecido")
	}
	if !isMissingPackageError(errors.New("No matching Packages to list")) {
		t.Fatal("erro de repositório vazio não reconhecido")
	}
	if isMissingPackageError(errors.New("dial tcp: i/o timeout")) {
		t.Fatal("erro de rede classificado como pacote ausente")
	}
	if isMissingPackageError(nil) {
		t.Fatal("nil classificado como pacote ausente")
	}
}

func TestDockerFetcherPrepareConfiguresRepositories(t *testing.T) {
	rt := archRuntime("amd64")
	var scripts []string
	rt.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return unameForTest("amd64"), nil
		}
		if len(cmd) >= 3 && cmd[0] == "sh" && cmd[1] == "-c" {
			scripts = append(scripts, cmd[2])
		}
		return "", nil
	}
	res := &fakeResolver{}
	f := newTestFetcher(t, rt, res)

	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("KEYDATA"), 0o600); err != nil {
		t.Fatalf("escrever chave: %v", err)
	}
	f.repositories = []core.PackageRepository{{
		Name: "Interno", URL: "https://repo.interno/apt", KeyFile: keyFile,
		Suite: "bookworm", Components: []string{"main"},
	}}

	if err := f.Prepare(context.Background(), "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	joined := strings.Join(scripts, "\n")
	if !strings.Contains(joined, "/etc/apt/sources.list.d/opf-42-interno.list") {
		t.Fatalf("repositório APT não configurado:\n%s", joined)
	}
	if !strings.Contains(joined, "/usr/share/keyrings/opf-42-interno.gpg") {
		t.Fatalf("chave APT não configurada:\n%s", joined)
	}
}

func TestDockerFetcherPrepareAppliesAPKRepositoryPolicy(t *testing.T) {
	rt := &fakeRuntime{id: "fake-container", execFn: func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return unameForTest("amd64"), nil
		}
		return "", nil
	}}
	res := &fakeResolver{}
	f := newTestFetcher(t, rt, res)
	f.repositories = []core.PackageRepository{{
		Name: "APK", URL: "https://repo.interno/alpine", InsecureSkipGPGCheck: true,
	}}

	if err := f.Prepare(context.Background(), "alpine", "3.20", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.untrusted {
		t.Fatal("resolver APK deveria receber SetAllowUntrusted(true)")
	}
}

type archiveFakeRuntime struct {
	*fakeRuntime
	archiveBytes []byte
	archiveCalls int
}

func (r *archiveFakeRuntime) CopyArchiveFromContainer(_ context.Context, _ string) (io.ReadCloser, error) {
	r.archiveCalls++
	return io.NopCloser(bytes.NewReader(r.archiveBytes)), nil
}

func buildTarArchive(t *testing.T, entries []struct {
	name     string
	content  string
	typeflag byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.content)),
			Typeflag: tf,
		}
		if tf == tar.TypeSymlink {
			hdr.Size = 0
			hdr.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%q): %v", e.name, err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatalf("Write(%q): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar: %v", err)
	}
	return buf.Bytes()
}

func TestDockerFetcherBulkArchiveCopyAndTarSlipGuard(t *testing.T) {
	vimContent := "conteudo-vim-bulk"
	libcContent := "conteudo-libc6-bulk"
	inventory := strings.Join([]string{
		"vim_9.0_amd64.deb\tvim\t9.0\tamd64\t" + debHash(vimContent),
		"libc6_2.35_amd64.deb\tlibc6\t2.35\tamd64\t" + debHash(libcContent),
	}, "\n")

	archive := buildTarArchive(t, []struct {
		name     string
		content  string
		typeflag byte
	}{
		{name: "opf-42/", typeflag: tar.TypeDir},
		{name: "../escape.deb", content: "malicioso"},
		{name: "opf-42/symlink.deb", typeflag: tar.TypeSymlink},
		{name: "opf-42/lists/vim_9.0_amd64.deb", content: "subdiretorio-errado"},
		{name: "opf-42/unexpected.deb", content: "nao-listado"},
		{name: "opf-42/vim_9.0_amd64.deb", content: vimContent},
		{name: "opf-42/libc6_2.35_amd64.deb", content: libcContent},
	})

	baseRT := archRuntime("amd64")
	baseRT.execFn = func(cmd []string) (string, error) {
		if strings.Join(cmd, " ") == "uname -m" {
			return "x86_64\n", nil
		}
		if cmd[0] == "sh" {
			return inventory, nil
		}
		return "", nil
	}
	rt := &archiveFakeRuntime{fakeRuntime: baseRT, archiveBytes: archive}
	res := &fakeResolver{
		deps:     []string{"libc6", "vim"},
		commands: [][]string{{"apt-get", "download", "libc6", "vim"}},
	}

	f := NewDockerFetcher(
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithResolverFactory(func(core.CommandExecutor, string, string) core.DependencyResolver { return res }),
	)
	f.factory = func(context.Context, runConfig) (containerRuntime, error) { return rt, nil }
	if err := f.BindSession(42); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	t.Cleanup(func() { _ = f.Cleanup(context.Background()) })

	var observedSteps []int
	f.SetStageObserver(func(step int) { observedSteps = append(observedSteps, step) })

	if err := f.Prepare(context.Background(), "ubuntu", "22.04", "amd64"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !f.IsPrepared() {
		t.Fatal("IsPrepared deveria ser true após Prepare")
	}

	artifacts, err := f.Download(context.Background(), "vim", false)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if rt.archiveCalls != 1 {
		t.Fatalf("archiveCalls = %d, esperava 1", rt.archiveCalls)
	}
	if len(baseRT.copied) != 0 {
		t.Fatalf("CopyFromContainer individual não deveria ser usado: %v", baseRT.copied)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artefatos = %d, esperava 2", len(artifacts))
	}
	if len(observedSteps) != 1 || observedSteps[0] != 5 {
		t.Fatalf("stage observer recebeu %v, esperava [5]", observedSteps)
	}
	if _, err := os.Stat(filepath.Join(f.HostDir(), "unexpected.deb")); !os.IsNotExist(err) {
		t.Fatal("arquivo inesperado não deveria ser extraído")
	}
	if _, err := os.Stat(filepath.Join(f.HostDir(), "symlink.deb")); !os.IsNotExist(err) {
		t.Fatal("symlink não deveria ser extraído")
	}
}
