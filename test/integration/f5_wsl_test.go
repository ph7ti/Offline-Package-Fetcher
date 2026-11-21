//go:build integration && windows

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubuntu/gowsl"

	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

func TestF5WSLFetcherCacheIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	distros, err := gowsl.RegisteredDistros(ctx)
	if err != nil {
		t.Fatalf("listar distros WSL: %v", err)
	}
	var distroName, version string
	for _, distro := range distros {
		if !strings.HasPrefix(strings.ToLower(distro.Name()), "ubuntu-") {
			continue
		}
		osRelease, err := wslCommand(ctx, distro.Name(), "cat /etc/os-release")
		if err != nil {
			continue
		}
		values := parseKeyValues(osRelease)
		if values["ID"] != "ubuntu" || values["VERSION_ID"] == "" {
			continue
		}
		distroName = distro.Name()
		version = values["VERSION_ID"]
		break
	}
	if distroName == "" {
		t.Skip("nenhuma distro Ubuntu registrada no WSL")
	}

	cacheBefore, err := wslCommand(ctx, distroName,
		"echo LISTS; find /var/lib/apt/lists -maxdepth 1 -type f -printf '%f %s\\n' 2>/dev/null | sort; "+
			"echo ARCHIVES; find /var/cache/apt/archives -maxdepth 1 -type f -printf '%f %s\\n' 2>/dev/null | sort")
	if err != nil {
		t.Fatalf("snapshot do cache APT antes: %v", err)
	}

	repo, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("abrir SQLite: %v", err)
	}
	defer repo.Close()

	wslFetcher := fetcher.NewWSLFetcher(
		fetcher.WithWSLDistro(distroName),
		fetcher.WithWSLResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			return resolver.NewAptResolver(exec, arch, workDir)
		}),
	)
	sessao := &core.Sessao{
		TargetOS:      "ubuntu",
		TargetVersion: version,
		TargetArch:    "amd64",
		FetchMethod:   "wsl",
	}
	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:          repo,
		Fetcher:       wslFetcher,
		Pacote:        "xxd",
		OutputBaseDir: t.TempDir(),
		StageTimeout:  10 * time.Minute,
	}, sessao)
	if err != nil {
		t.Fatalf("pipeline WSL: %v", err)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("nenhum artefato WSL")
	}
	if _, err := transfer.VerificarManifesto(result.CaminhoManifesto, result.OutputDir); err != nil {
		t.Fatalf("verificar manifesto WSL: %v", err)
	}
	pacotes, err := repo.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(pacotes) != len(result.Artifacts) {
		t.Fatalf("pacotes no SQLite = %d, artefatos = %d", len(pacotes), len(result.Artifacts))
	}
	for _, pacote := range pacotes {
		if pacote.Status != core.PacoteBaixado {
			t.Fatalf("pacote %q com status %q", pacote.Nome, pacote.Status)
		}
	}
	stored, err := repo.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.Status != core.SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
	if wslFetcher.HostDir() != "" {
		t.Fatalf("diretório host do WSLFetcher não foi limpo: %s", wslFetcher.HostDir())
	}
	if _, err := wslCommand(ctx, distroName, fmt.Sprintf("test ! -e /tmp/opf-%d", sessao.ID)); err != nil {
		t.Fatalf("workdir WSL não foi removido: %v", err)
	}

	cacheAfter, err := wslCommand(ctx, distroName,
		"echo LISTS; find /var/lib/apt/lists -maxdepth 1 -type f -printf '%f %s\\n' 2>/dev/null | sort; "+
			"echo ARCHIVES; find /var/cache/apt/archives -maxdepth 1 -type f -printf '%f %s\\n' 2>/dev/null | sort")
	if err != nil {
		t.Fatalf("snapshot do cache APT depois: %v", err)
	}
	if cacheBefore != cacheAfter {
		t.Fatalf("cache APT do usuário foi alterado:\nANTES:\n%s\nDEPOIS:\n%s", cacheBefore, cacheAfter)
	}
}

func wslCommand(ctx context.Context, distro, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "-u", "root", "--", "sh", "-c", script)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func parseKeyValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(value, `"'`)
	}
	return values
}
