package core

import (
	"context"
	"errors"
	"testing"
)

type stageObservingFetcher struct {
	fakeFetcher
	observer StageObserver
}

func (f *stageObservingFetcher) SetStageObserver(observer StageObserver) {
	f.observer = observer
}

func (f *stageObservingFetcher) Download(ctx context.Context, pkg string, includeOptional bool) ([]PackageArtifact, error) {
	if f.observer != nil {
		f.observer(5)
	}
	return f.fakeFetcher.Download(ctx, pkg, includeOptional)
}

func TestPipelineStageObserverMarksStep5OnDownloadFailure(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &stageObservingFetcher{
		fakeFetcher: fakeFetcher{
			sourceDir:   t.TempDir(),
			downloadErr: errors.New("falha simulada no download"),
		},
	}
	sessao := &Sessao{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "docker",
	}

	_, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
	}, sessao)
	if err == nil {
		t.Fatal("esperava erro no download")
	}

	etapas, listErr := repo.ListarEtapas(context.Background(), sessao.ID)
	if listErr != nil {
		t.Fatalf("ListarEtapas: %v", listErr)
	}
	byStep := make(map[int]EtapaStatus, len(etapas))
	for _, e := range etapas {
		byStep[e.Etapa] = e.Status
	}
	if byStep[4] != EtapaConcluida {
		t.Fatalf("etapa 4 = %q, esperava concluida", byStep[4])
	}
	if byStep[5] != EtapaFalhou {
		t.Fatalf("etapa 5 = %q, esperava falhou", byStep[5])
	}
}
