//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/go-sdk/client"
	"github.com/docker/go-sdk/image"

	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

func TestF0Ubuntu2204Vim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolver raiz do projeto: %v", err)
	}
	dockerDir := filepath.Join(root, "docker")
	tag := "opf/ubuntu-22.04:f0"

	if _, err := image.BuildFromDir(ctx, dockerDir, "ubuntu-22.04.Dockerfile", tag); err != nil {
		t.Fatalf("build da imagem de teste: %v", err)
	}

	repo, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("abrir SQLite: %v", err)
	}
	defer repo.Close()

	outputBase := t.TempDir()
	f := fetcher.NewDockerFetcher(
		fetcher.WithImage(tag),
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewAptResolver(exec, arch, workDir)
		}),
	)

	sessao := &core.Sessao{
		TargetOS:        "ubuntu",
		TargetVersion:   "22.04",
		TargetArch:      "amd64",
		FetchMethod:     "docker",
		IncludeOptional: false,
	}

	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       f,
		Pacote:        "vim",
		OutputBaseDir: outputBase,
		StageTimeout:  15 * time.Minute,
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline F0: %v", err)
	}
	if result == nil {
		t.Fatal("resultado nulo")
	}

	containerID := f.ContainerID()
	if containerID == "" {
		t.Fatal("id do contêiner não registrado")
	}

	verified, err := transfer.VerificarManifesto(result.CaminhoManifesto, result.OutputDir)
	if err != nil {
		t.Fatalf("verificar SHA256SUMS: %v", err)
	}
	if len(verified.Arquivos) != len(result.Artifacts) {
		t.Fatalf("manifesto tem %d entradas, esperava %d", len(verified.Arquivos), len(result.Artifacts))
	}
	if len(result.Artifacts) < 10 {
		t.Fatalf("poucos artefatos baixados: %d", len(result.Artifacts))
	}

	names := make(map[string]core.PackageArtifact, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		names[artifact.Name] = artifact
		if artifact.Version == "" || (artifact.Arch != "amd64" && artifact.Arch != "all") || artifact.SHA256 == "" {
			t.Fatalf("metadados incompletos do artefato: %+v", artifact)
		}
		if _, err := os.Stat(artifact.LocalPath); err != nil {
			t.Fatalf("artefato ausente no host: %v", err)
		}
		if filepath.Dir(artifact.LocalPath) != result.OutputDir {
			t.Fatalf("artefato fora do diretório de saída: %s", artifact.LocalPath)
		}
		hash, err := transfer.HashFile(artifact.LocalPath)
		if err != nil {
			t.Fatalf("hash do artefato %q: %v", artifact.Name, err)
		}
		if hash != artifact.SHA256 {
			t.Fatalf("hash divergente para %q", artifact.Name)
		}
	}
	for _, required := range []string{"vim", "vim-common", "vim-runtime", "libc6", "libpython3.10"} {
		artifact, ok := names[required]
		if !ok {
			t.Fatalf("pacote obrigatório %q não baixado (artefatos: %d)", required, len(result.Artifacts))
		}
		if required == "vim" && artifact.IsDependency {
			t.Fatalf("pacote principal %q marcado como dependência", required)
		}
		if required != "vim" && !artifact.IsDependency {
			t.Fatalf("dependência %q não marcada como dependência", required)
		}
	}

	stored, err := repo.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.Status != core.SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
	if stored.FinalizadaEm == nil {
		t.Fatal("sessão sem finalizada_em")
	}

	pacotes, err := repo.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(pacotes) != len(result.Artifacts) {
		t.Fatalf("SQLite tem %d pacotes, esperava %d", len(pacotes), len(result.Artifacts))
	}
	for _, pacote := range pacotes {
		if pacote.Status != core.PacoteBaixado || pacote.SHA256 == "" || pacote.CaminhoLocal == "" {
			t.Fatalf("registro de pacote incompleto: %+v", pacote)
		}
	}
	eventos, err := repo.ListarEventos(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	if len(eventos) == 0 {
		t.Fatal("nenhum evento registrado")
	}

	if f.HostDir() != "" {
		t.Fatalf("diretório temporário do host não foi limpo: %s", f.HostDir())
	}
	stale, err := filepath.Glob(filepath.Join(os.TempDir(), fmt.Sprintf("opf-%d-*", sessao.ID)))
	if err != nil {
		t.Fatalf("procurar diretórios temporários residuais: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("diretórios temporários residuais: %v", stale)
	}

	cli, err := client.New(ctx)
	if err != nil {
		t.Fatalf("cliente Docker: %v", err)
	}
	if summary, err := cli.FindContainerByID(ctx, containerID); err == nil && summary != nil {
		t.Fatalf("contêiner %s ainda existe após o cleanup", containerID)
	}
}
