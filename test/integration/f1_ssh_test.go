//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-sdk/container"
	"github.com/docker/go-sdk/container/wait"
	"github.com/docker/go-sdk/image"
	mobycontainer "github.com/moby/moby/api/types/container"
	apinetwork "github.com/moby/moby/api/types/network"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	opfapp "opf/internal/app"
	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/inspector"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

const sshdDockerfile = `FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-server ca-certificates sudo \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -s /bin/bash opf \
    && echo 'opf:opfpass' | chpasswd \
    && printf 'Defaults:opf !requiretty\nopf ALL=(ALL:ALL) ALL\n' > /etc/sudoers.d/opf \
    && chmod 440 /etc/sudoers.d/opf \
    && mkdir -p /home/opf/.ssh /root/.ssh /run/sshd \
    && chmod 700 /home/opf/.ssh /root/.ssh \
    && ssh-keygen -A \
    && sed -i 's/^#*PasswordAuthentication .*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
    && sed -i 's/^#*KbdInteractiveAuthentication .*/KbdInteractiveAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/^#*PermitRootLogin .*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
    && printf '\nPubkeyAuthentication yes\nAllowTcpForwarding yes\n' >> /etc/ssh/sshd_config
COPY authorized_keys /home/opf/.ssh/authorized_keys
COPY authorized_keys /root/.ssh/authorized_keys
RUN chown -R opf:opf /home/opf/.ssh && chmod 600 /home/opf/.ssh/authorized_keys \
    && chown -R root:root /root/.ssh && chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
`

func TestF1SSHTransferWithJump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	clientKeyPath, authorizedKey := generateClientKey(t)
	sshdImage := buildSSHDImage(ctx, t, authorizedKey)

	destination := runSSHD(ctx, t, sshdImage, "destination")
	jump := runSSHD(ctx, t, sshdImage, "jump")

	destinationPort := mappedPort(ctx, t, destination)
	jumpPort := mappedPort(ctx, t, jump)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	var knownHostsLines []string
	knownHostsLines = append(knownHostsLines, hostKeyLines(ctx, t, destination, fmt.Sprintf("127.0.0.1:%d", destinationPort))...)
	knownHostsLines = append(knownHostsLines, hostKeyLines(ctx, t, destination, fmt.Sprintf("host.docker.internal:%d", destinationPort))...)
	knownHostsLines = append(knownHostsLines, hostKeyLines(ctx, t, jump, fmt.Sprintf("127.0.0.1:%d", jumpPort))...)
	if err := os.WriteFile(knownHostsPath, []byte(strings.Join(knownHostsLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("escrever known_hosts: %v", err)
	}

	directSSH := core.SSHConfig{
		Host:       "127.0.0.1",
		Port:       destinationPort,
		User:       "opf",
		KeyPath:    clientKeyPath,
		KnownHosts: knownHostsPath,
	}
	jumpSSH := core.SSHConfig{
		Host:       "host.docker.internal",
		Port:       destinationPort,
		User:       "opf",
		KeyPath:    clientKeyPath,
		KnownHosts: knownHostsPath,
		JumpHosts: []core.SSHConfig{{
			Host:       "127.0.0.1",
			Port:       jumpPort,
			User:       "opf",
			KeyPath:    clientKeyPath,
			KnownHosts: knownHostsPath,
		}},
	}

	detected, err := opfapp.DetectRemoteTarget(ctx, directSSH)
	if err != nil {
		t.Fatalf("detectar target via SSH: %v", err)
	}
	if detected.OS != "ubuntu" || detected.Version != "22.04" || detected.Arch != "amd64" || detected.Family != "apt" {
		t.Fatalf("target detectado inesperado: %+v", detected)
	}

	repo, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("abrir SQLite: %v", err)
	}
	defer repo.Close()

	dockerFetcher := fetcher.NewDockerFetcher(
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewAptResolver(exec, arch, workDir)
		}),
	)
	transferManager := transfer.NewSSHTransferManager()
	pkgInspector, err := inspector.New("apt", transferManager)
	if err != nil {
		t.Fatalf("inspector: %v", err)
	}
	remoteDir := fmt.Sprintf("/tmp/opf-f1-%d", time.Now().UnixNano())

	sessao := &core.Sessao{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "docker",
	}
	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       dockerFetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  15 * time.Minute,
		Transfer:      transferManager,
		Inspector:     pkgInspector,
		SSH:           directSSH,
		RemoteDir:     remoteDir,
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline F1: %v", err)
	}
	if result.RemoteDir != remoteDir {
		t.Fatalf("remote dir = %q", result.RemoteDir)
	}

	pacotes, err := repo.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(pacotes) != len(result.Artifacts) {
		t.Fatalf("pacotes no SQLite = %d, artefatos = %d", len(pacotes), len(result.Artifacts))
	}
	for _, pacote := range pacotes {
		if pacote.Status != core.PacoteEnviado {
			t.Fatalf("pacote %q com status %q, esperava enviado", pacote.Nome, pacote.Status)
		}
	}
	stored, err := repo.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.Status != core.SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
	if stored.HostInfoJSON == "" {
		t.Fatal("host info do destino não coletada")
	}

	verifyManager := transfer.NewSSHTransferManager()
	if err := verifyManager.Connect(ctx, directSSH); err != nil {
		t.Fatalf("conectar para verificação: %v", err)
	}
	defer verifyManager.Close()

	output, err := verifyManager.RunCommand(ctx,
		fmt.Sprintf("cd %s && ls -1 *.deb | wc -l", remoteDir))
	if err != nil {
		t.Fatalf("contar .deb remotos: %v", err)
	}
	if got := strings.TrimSpace(output); got != fmt.Sprintf("%d", len(result.Artifacts)) {
		t.Fatalf(".deb remotos = %s, esperava %d", got, len(result.Artifacts))
	}
	output, err = verifyManager.RunCommand(ctx,
		fmt.Sprintf("cd %s && sha256sum -c SHA256SUMS", remoteDir))
	if err != nil {
		t.Fatalf("sha256sum -c remoto: %v\n%s", err, output)
	}
	if strings.Count(output, ": OK") != len(result.Artifacts) {
		t.Fatalf("saída do sha256sum -c inesperada: %s", output)
	}

	// Host key desconhecida deve abortar a conexão.
	emptyKnownHosts := filepath.Join(t.TempDir(), "empty_known_hosts")
	if err := os.WriteFile(emptyKnownHosts, nil, 0o600); err != nil {
		t.Fatalf("criar known_hosts vazio: %v", err)
	}
	badManager := transfer.NewSSHTransferManager()
	badCfg := directSSH
	badCfg.KnownHosts = emptyKnownHosts
	if err := badManager.Connect(ctx, badCfg); err == nil {
		t.Fatal("conexão deveria falhar com host key desconhecida")
	} else if !strings.Contains(strings.ToLower(err.Error()), "host key") {
		t.Fatalf("erro deveria apontar host key: %v", err)
	}

	skipManager := transfer.NewSSHTransferManager()
	skipCfg := badCfg
	skipCfg.InsecureSkipHostKey = true
	if err := skipManager.Connect(ctx, skipCfg); err != nil {
		t.Fatalf("conexão com skip de host key deveria funcionar: %v", err)
	}
	_ = skipManager.Close()

	// Upload/verificação através de um jump host.
	jumpDir := fmt.Sprintf("/tmp/opf-f1-jump-%d", time.Now().UnixNano())
	localDir := t.TempDir()
	fileA := filepath.Join(localDir, "a.txt")
	fileB := filepath.Join(localDir, "b.txt")
	if err := os.WriteFile(fileA, []byte("conteudo-a"), 0o644); err != nil {
		t.Fatalf("criar arquivo A: %v", err)
	}
	if err := os.WriteFile(fileB, []byte("conteudo-b"), 0o644); err != nil {
		t.Fatalf("criar arquivo B: %v", err)
	}
	manifest, err := transfer.GerarManifesto(localDir, []string{fileA, fileB})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}

	jumpManager := transfer.NewSSHTransferManager()
	if err := jumpManager.Connect(ctx, jumpSSH); err != nil {
		t.Fatalf("conectar via jump: %v", err)
	}
	defer jumpManager.Close()
	if err := jumpManager.Upload(ctx, []string{fileA, fileB, manifest.Caminho}, jumpDir); err != nil {
		t.Fatalf("upload via jump: %v", err)
	}
	if err := jumpManager.VerifyChecksums(ctx, path.Join(jumpDir, transfer.NomeManifesto), jumpDir); err != nil {
		t.Fatalf("verificação via jump: %v", err)
	}
	output, err = jumpManager.RunCommand(ctx, fmt.Sprintf("cd %s && sha256sum -c SHA256SUMS", jumpDir))
	if err != nil {
		t.Fatalf("sha256sum -c via jump: %v\n%s", err, output)
	}
	if strings.Count(output, ": OK") != 2 {
		t.Fatalf("saída do sha256sum -c via jump inesperada: %s", output)
	}
}

func generateClientKey(t *testing.T) (string, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gerar chave do cliente: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("criar signer do cliente: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("serializar chave do cliente: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("escrever chave do cliente: %v", err)
	}
	return keyPath, string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
}

func buildSSHDImage(ctx context.Context, t *testing.T, authorizedKey string) string {
	t.Helper()
	contextDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(sshdDockerfile), 0o644); err != nil {
		t.Fatalf("escrever Dockerfile sshd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "authorized_keys"), []byte(authorizedKey), 0o644); err != nil {
		t.Fatalf("escrever authorized_keys: %v", err)
	}
	tag := fmt.Sprintf("opf/sshd-f1:%d", time.Now().UnixNano())
	if _, err := image.BuildFromDir(ctx, contextDir, "Dockerfile", tag); err != nil {
		t.Fatalf("build da imagem sshd: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := image.Remove(cleanupCtx, tag); err != nil {
			t.Logf("remover imagem sshd %s: %v", tag, err)
		}
	})
	return tag
}

func runSSHD(ctx context.Context, t *testing.T, imageTag, name string) *container.Container {
	t.Helper()
	options := []container.ContainerCustomizer{
		container.WithImage(imageTag),
		container.WithName(fmt.Sprintf("opf-f1-%s-%d", name, time.Now().UnixNano())),
		container.WithExposedPorts("22/tcp"),
		container.WithWaitStrategy(wait.ForListeningPort(apinetwork.MustParsePort("22/tcp"))),
	}
	if runtime.GOOS == "linux" {
		options = append(options, container.WithHostConfigModifier(func(hostConfig *mobycontainer.HostConfig) {
			hostConfig.ExtraHosts = append(hostConfig.ExtraHosts, "host.docker.internal:host-gateway")
		}))
	}
	ctr, err := container.Run(ctx, options...)
	if err != nil {
		t.Fatalf("subir sshd %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ctr.Terminate(cleanupCtx); err != nil {
			t.Logf("terminar sshd %s: %v", name, err)
		}
	})
	return ctr
}

func mappedPort(ctx context.Context, t *testing.T, ctr *container.Container) int {
	t.Helper()
	port, err := ctr.MappedPort(ctx, apinetwork.MustParsePort("22/tcp"))
	if err != nil {
		t.Fatalf("porta mapeada do sshd: %v", err)
	}
	return int(port.Num())
}

func hostKeyLines(ctx context.Context, t *testing.T, ctr *container.Container, address string) []string {
	t.Helper()
	candidates := []string{
		"/etc/ssh/ssh_host_ed25519_key.pub",
		"/etc/ssh/ssh_host_ecdsa_key.pub",
		"/etc/ssh/ssh_host_rsa_key.pub",
	}
	var lines []string
	for _, candidate := range candidates {
		reader, err := ctr.CopyFromContainer(ctx, candidate)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			t.Fatalf("ler host key %s: %v", candidate, readErr)
		}
		publicKey, _, _, _, parseErr := ssh.ParseAuthorizedKey(data)
		if parseErr != nil {
			t.Fatalf("interpretar host key %s: %v", candidate, parseErr)
		}
		lines = append(lines, knownhosts.Line([]string{knownhosts.Normalize(address)}, publicKey))
	}
	if len(lines) == 0 {
		t.Fatalf("nenhuma host key encontrada em %s", address)
	}
	return lines
}
