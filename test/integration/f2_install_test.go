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

	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/inspector"
	"opf/internal/installer"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

func TestF2AptInstallWithConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	_, authorizedKey := generateClientKey(t)
	sshdImage := buildSSHDImage(ctx, t, authorizedKey)
	destination := runSSHD(ctx, t, sshdImage, "f2-destination")
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
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewAptResolver(exec, arch, workDir)
		}),
	)
	confirmedArtifacts := 0
	remoteDir := fmt.Sprintf("/tmp/opf-f2-%d", time.Now().UnixNano())
	sessao := &core.Sessao{
		TargetOS:        "ubuntu",
		TargetVersion:   "22.04",
		TargetArch:      "amd64",
		FetchMethod:     "docker",
		IncludeOptional: false,
	}

	transferManager := transfer.NewSSHTransferManager()
	pkgInspector, err := inspector.New("apt", transferManager)
	if err != nil {
		t.Fatalf("inspector: %v", err)
	}
	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       dockerFetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  15 * time.Minute,
		Transfer:      transferManager,
		Inspector:     pkgInspector,
		SSH:           sshCfg,
		RemoteDir:     remoteDir,
		Installer:     installer.NewAptInstaller(installer.WithSudoPassword("opfpass")),
		ConfirmInstall: func(_ context.Context, artifacts []core.PackageArtifact) (bool, error) {
			confirmedArtifacts = len(artifacts)
			return true, nil
		},
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline F2: %v", err)
	}
	if confirmedArtifacts != len(result.Artifacts) || confirmedArtifacts == 0 {
		t.Fatalf("artefatos confirmados = %d, esperava %d", confirmedArtifacts, len(result.Artifacts))
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
	eventos, err := repo.ListarEventos(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	hasInstall := false
	for _, evento := range eventos {
		if evento.Tipo == core.EventoInstalacao {
			hasInstall = true
		}
	}
	if !hasInstall {
		t.Fatal("nenhum evento de instalação registrado")
	}

	verifyManager := transfer.NewSSHTransferManager()
	if err := verifyManager.Connect(ctx, sshCfg); err != nil {
		t.Fatalf("conectar para verificar instalação: %v", err)
	}
	defer verifyManager.Close()
	output, err := verifyManager.RunCommand(ctx, "dpkg-query -W -f='${Status}' vim")
	if err != nil {
		t.Fatalf("dpkg-query vim: %v\n%s", err, output)
	}
	if !strings.Contains(output, "install ok installed") {
		t.Fatalf("vim não está instalado: %s", output)
	}
	output, err = verifyManager.RunCommand(ctx, "test -x /usr/bin/vim && echo VIM_OK")
	if err != nil || !strings.Contains(output, "VIM_OK") {
		t.Fatalf("executável vim ausente: %v\n%s", err, output)
	}

	// Segunda sessão: usuário recusa a instalação.
	declineDir := fmt.Sprintf("/tmp/opf-f2-decline-%d", time.Now().UnixNano())
	sessaoRecusada := &core.Sessao{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "docker",
	}
	secondFetcher := fetcher.NewDockerFetcher(
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewAptResolver(exec, arch, workDir)
		}),
	)
	secondTransfer := transfer.NewSSHTransferManager()
	secondInspector, err := inspector.New("apt", secondTransfer)
	if err != nil {
		t.Fatalf("inspector 2: %v", err)
	}
	resultDeclined, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       secondFetcher,
		Pacote:        "nano",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  15 * time.Minute,
		Transfer:      secondTransfer,
		Inspector:     secondInspector,
		SSH:           sshCfg,
		RemoteDir:     declineDir,
		Installer:     installer.NewAptInstaller(installer.WithSudoPassword("opfpass")),
		ConfirmInstall: func(context.Context, []core.PackageArtifact) (bool, error) {
			return false, nil
		},
	}, sessaoRecusada)
	if err != nil {
		t.Fatalf("pipeline F2 recusado: %v", err)
	}
	if resultDeclined.Installed || !resultDeclined.InstallDeclined {
		t.Fatalf("resultado da recusa: %+v", resultDeclined)
	}
	pacotesRecusados, err := repo.ListarPacotes(ctx, sessaoRecusada.ID)
	if err != nil {
		t.Fatalf("ListarPacotes recusados: %v", err)
	}
	for _, pacote := range pacotesRecusados {
		if pacote.Status != core.PacoteEnviadoNaoInstalado {
			t.Fatalf("pacote recusado %q com status %q", pacote.Nome, pacote.Status)
		}
	}
	output, err = verifyManager.RunCommand(ctx, "dpkg-query -W -f='${Status}' nano")
	if err == nil && !strings.Contains(output, "not-installed") {
		t.Fatalf("nano não deveria ter sido instalado (saída=%q)", output)
	}
	if err != nil {
		t.Logf("dpkg-query nano retornou erro esperado: %v (%q)", err, output)
	}
}
