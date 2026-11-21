//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-sdk/image"

	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/inspector"
	"opf/internal/installer"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

func TestF3YumDnfInstallWithSudo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolver raiz do projeto: %v", err)
	}
	baseTag := "opf/centos-7:f3"
	if _, err := image.BuildFromDir(ctx, filepath.Join(root, "docker"), "centos-7.Dockerfile", baseTag); err != nil {
		t.Fatalf("build da imagem CentOS 7: %v", err)
	}

	_, authorizedKey := generateClientKey(t)
	sshdTag := buildCentOSSSHDImage(ctx, t, baseTag, authorizedKey)
	destination := runSSHD(ctx, t, sshdTag, "f3-destination")
	destinationPort := mappedPort(ctx, t, destination)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	lines := hostKeyLines(ctx, t, destination, fmt.Sprintf("127.0.0.1:%d", destinationPort))
	if err := os.WriteFile(knownHostsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("escrever known_hosts: %v", err)
	}
	sshCfg := core.SSHConfig{
		Host:       "127.0.0.1",
		Port:       destinationPort,
		User:       "opf",
		Password:   "opfpass",
		KnownHosts: knownHostsPath,
	}

	repo, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("abrir SQLite: %v", err)
	}
	defer repo.Close()

	dockerFetcher := fetcher.NewDockerFetcher(
		fetcher.WithImage(baseTag),
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewYumDnfResolver(exec, arch, workDir)
		}),
	)
	remoteDir := fmt.Sprintf("/tmp/opf-f3-%d", time.Now().UnixNano())
	sessao := &core.Sessao{
		TargetOS:      "centos",
		TargetVersion: "7",
		TargetArch:    "x86_64",
		FetchMethod:   "docker",
	}
	confirmedArtifacts := 0

	transferManager := transfer.NewSSHTransferManager()
	pkgInspector, err := inspector.New("rpm", transferManager)
	if err != nil {
		t.Fatalf("inspector: %v", err)
	}
	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       dockerFetcher,
		Pacote:        "vim-enhanced",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  20 * time.Minute,
		Transfer:      transferManager,
		Inspector:     pkgInspector,
		SSH:           sshCfg,
		RemoteDir:     remoteDir,
		Installer:     installer.NewYumDnfInstaller(installer.WithSudoPassword("opfpass")),
		ConfirmInstall: func(_ context.Context, artifacts []core.PackageArtifact) (bool, error) {
			confirmedArtifacts = len(artifacts)
			return true, nil
		},
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline F3: %v", err)
	}
	if confirmedArtifacts == 0 || confirmedArtifacts != len(result.Artifacts) {
		t.Fatalf("artefatos confirmados = %d, esperava %d", confirmedArtifacts, len(result.Artifacts))
	}
	if len(result.Artifacts) < 10 {
		t.Fatalf("poucos artefatos RPM: %d", len(result.Artifacts))
	}
	if !result.Installed || result.InstallDeclined {
		t.Fatalf("resultado de instalação: %+v", result)
	}

	pacotes, err := repo.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(pacotes) != len(result.Artifacts) {
		t.Fatalf("pacotes no SQLite = %d, artefatos = %d", len(pacotes), len(result.Artifacts))
	}
	for _, pacote := range pacotes {
		if pacote.Status != core.PacoteInstalado {
			t.Fatalf("pacote %q com status %q, esperava instalado", pacote.Nome, pacote.Status)
		}
	}
	stored, err := repo.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.Status != core.SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}

	verifyManager := transfer.NewSSHTransferManager()
	if err := verifyManager.Connect(ctx, sshCfg); err != nil {
		t.Fatalf("conectar para verificar instalação: %v", err)
	}
	defer verifyManager.Close()

	output, err := verifyManager.RunCommand(ctx, "rpm -q vim-enhanced")
	if err != nil {
		t.Fatalf("rpm -q vim-enhanced: %v\n%s", err, output)
	}
	if !strings.Contains(output, "vim-enhanced-") {
		t.Fatalf("vim-enhanced não instalado: %s", output)
	}
	output, err = verifyManager.RunCommand(ctx, "test -x /usr/bin/vim && echo VIM_OK")
	if err != nil || !strings.Contains(output, "VIM_OK") {
		t.Fatalf("executável vim ausente: %v\n%s", err, output)
	}
	output, err = verifyManager.RunCommand(ctx, fmt.Sprintf("cd %s && ls -1 *.rpm | wc -l", remoteDir))
	if err != nil {
		t.Fatalf("contar RPMs remotos: %v", err)
	}
	if got := strings.TrimSpace(output); got != fmt.Sprintf("%d", len(result.Artifacts)) {
		t.Fatalf("RPMs remotos = %s, esperava %d", got, len(result.Artifacts))
	}
}

func buildCentOSSSHDImage(ctx context.Context, t *testing.T, baseTag, authorizedKey string) string {
	t.Helper()
	contextDir := t.TempDir()
	dockerfile := fmt.Sprintf(`FROM %s
RUN yum -y install openssh-server sudo \
    && useradd -m -s /bin/bash opf \
    && echo 'opf:opfpass' | chpasswd \
    && printf 'Defaults:opf !requiretty\nopf ALL=(ALL:ALL) ALL\n' > /etc/sudoers.d/opf \
    && chmod 440 /etc/sudoers.d/opf \
    && mkdir -p /home/opf/.ssh /run/sshd \
    && chmod 700 /home/opf/.ssh \
    && ssh-keygen -A \
    && sed -i 's/^#*PasswordAuthentication .*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
    && sed -i 's/^#*PermitRootLogin .*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
    && printf '\nPubkeyAuthentication yes\n' >> /etc/ssh/sshd_config
COPY authorized_keys /home/opf/.ssh/authorized_keys
RUN chown -R opf:opf /home/opf/.ssh && chmod 600 /home/opf/.ssh/authorized_keys
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
`, baseTag)
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("escrever Dockerfile CentOS sshd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "authorized_keys"), []byte(authorizedKey), 0o644); err != nil {
		t.Fatalf("escrever authorized_keys: %v", err)
	}
	tag := fmt.Sprintf("opf/centos-7-sshd-f3:%d", time.Now().UnixNano())
	if _, err := image.BuildFromDir(ctx, contextDir, "Dockerfile", tag); err != nil {
		t.Fatalf("build da imagem CentOS sshd: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := image.Remove(cleanupCtx, tag); err != nil {
			t.Logf("remover imagem CentOS sshd %s: %v", tag, err)
		}
	})
	return tag
}
