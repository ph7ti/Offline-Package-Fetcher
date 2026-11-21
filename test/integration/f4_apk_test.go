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

type f4Reporter struct {
	steps []int
	logs  []string
}

func (r *f4Reporter) StepStarted(step int, _ string) { r.steps = append(r.steps, step) }
func (r *f4Reporter) StepFinished(int, string)       {}
func (r *f4Reporter) StepFailed(int, string, error)  {}
func (r *f4Reporter) Progress(string, int64, int64)  {}
func (r *f4Reporter) Log(_, message string)          { r.logs = append(r.logs, message) }

func TestF4ApkInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolver raiz do projeto: %v", err)
	}
	baseTag := "opf/alpine-3.20:f4"
	if _, err := image.BuildFromDir(ctx, filepath.Join(root, "docker"), "alpine-3.20.Dockerfile", baseTag); err != nil {
		t.Fatalf("build da imagem Alpine: %v", err)
	}

	clientKeyPath, authorizedKey := generateClientKey(t)
	sshdTag := buildAlpineSSHDImage(ctx, t, baseTag, authorizedKey)
	destination := runSSHD(ctx, t, sshdTag, "f4-destination")
	destinationPort := mappedPort(ctx, t, destination)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	lines := hostKeyLines(ctx, t, destination, fmt.Sprintf("127.0.0.1:%d", destinationPort))
	if err := os.WriteFile(knownHostsPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("escrever known_hosts: %v", err)
	}
	sshCfg := core.SSHConfig{
		Host:       "127.0.0.1",
		Port:       destinationPort,
		User:       "root",
		KeyPath:    clientKeyPath,
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
			return resolver.NewApkResolver(exec, arch, workDir)
		}),
	)
	reporter := &f4Reporter{}
	remoteDir := fmt.Sprintf("/tmp/opf-f4-%d", time.Now().UnixNano())
	sessao := &core.Sessao{
		TargetOS:      "alpine",
		TargetVersion: "3.20",
		TargetArch:    "x86_64",
		FetchMethod:   "docker",
	}

	transferManager := transfer.NewSSHTransferManager()
	pkgInspector, err := inspector.New("apk", transferManager)
	if err != nil {
		t.Fatalf("inspector: %v", err)
	}
	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       dockerFetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  10 * time.Minute,
		Transfer:      transferManager,
		Inspector:     pkgInspector,
		SSH:           sshCfg,
		RemoteDir:     remoteDir,
		Installer:     installer.NewApkInstaller(installer.WithReporter(reporter)),
		Reporter:      reporter,
		ConfirmInstall: func(context.Context, []core.PackageArtifact) (bool, error) {
			return true, nil
		},
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline F4: %v", err)
	}
	if len(result.Artifacts) < 3 {
		t.Fatalf("poucos artefatos APK: %d", len(result.Artifacts))
	}
	if !result.Installed || result.InstallDeclined {
		t.Fatalf("resultado de instalação: %+v", result)
	}
	if len(reporter.steps) == 0 || len(reporter.logs) == 0 {
		t.Fatalf("reporter não recebeu etapas/logs: steps=%v logs=%d", reporter.steps, len(reporter.logs))
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

	output, err := verifyManager.RunCommand(ctx, "apk info -e vim")
	if err != nil {
		t.Fatalf("apk info -e vim: %v\n%s", err, output)
	}
	if strings.TrimSpace(output) != "vim" {
		t.Fatalf("vim não está instalado: %q", output)
	}
	output, err = verifyManager.RunCommand(ctx, "test -x /usr/bin/vim && echo VIM_OK")
	if err != nil || !strings.Contains(output, "VIM_OK") {
		t.Fatalf("executável vim ausente: %v\n%s", err, output)
	}
	output, err = verifyManager.RunCommand(ctx, fmt.Sprintf("cd %s && ls -1 *.apk | wc -l", remoteDir))
	if err != nil {
		t.Fatalf("contar APKs remotos: %v", err)
	}
	if got := strings.TrimSpace(output); got != fmt.Sprintf("%d", len(result.Artifacts)) {
		t.Fatalf("APKs remotos = %s, esperava %d", got, len(result.Artifacts))
	}
}

func buildAlpineSSHDImage(ctx context.Context, t *testing.T, baseTag, authorizedKey string) string {
	t.Helper()
	contextDir := t.TempDir()
	dockerfile := fmt.Sprintf(`FROM %s
RUN apk add --no-cache openssh-server \
    && ssh-keygen -A \
    && mkdir -p /root/.ssh /run/sshd \
    && chmod 700 /root/.ssh \
    && sed -i 's/^#*PermitRootLogin .*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
    && sed -i 's/^#*PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && printf '\nPubkeyAuthentication yes\n' >> /etc/ssh/sshd_config
COPY authorized_keys /root/.ssh/authorized_keys
RUN chmod 600 /root/.ssh/authorized_keys
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
`, baseTag)
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("escrever Dockerfile Alpine sshd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "authorized_keys"), []byte(authorizedKey), 0o644); err != nil {
		t.Fatalf("escrever authorized_keys: %v", err)
	}
	tag := fmt.Sprintf("opf/alpine-3.20-sshd-f4:%d", time.Now().UnixNano())
	if _, err := image.BuildFromDir(ctx, contextDir, "Dockerfile", tag); err != nil {
		t.Fatalf("build da imagem Alpine sshd: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := image.Remove(cleanupCtx, tag); err != nil {
			t.Logf("remover imagem Alpine sshd %s: %v", tag, err)
		}
	})
	return tag
}
