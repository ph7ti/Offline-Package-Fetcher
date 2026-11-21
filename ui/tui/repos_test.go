package tui

import (
	"strings"
	"testing"

	"opf/internal/core"
)

func newRepoTestModel() *Model {
	m := &Model{
		targetInput:           input("ubuntu", ""),
		repoNameEdit:          input("", ""),
		repoURLEdit:           input("", ""),
		repoKeyValueEdit:      input("", ""),
		repoCAEdit:            input("", ""),
		repoAPTSuiteEdit:      input("", ""),
		repoAPTComponentsEdit: input("main", ""),
	}
	m.repoKeySourceEdit = repoKeySourceNone
	return m
}

func TestRepoDraftKeySources(t *testing.T) {
	base := repoDraft{name: "Interno", family: core.RepositoryFamilyRPM, url: "https://repo.interno/rpm"}

	file := base
	file.keySource = repoKeySourceFile
	file.keyValue = "/tmp/key"
	if repo := file.repository(); repo.KeyFile != "/tmp/key" || repo.KeyURL != "" || repo.InsecureSkipGPGCheck {
		t.Fatalf("arquivo = %+v", repo)
	}

	url := base
	url.keySource = repoKeySourceURL
	url.keyValue = "https://repo.interno/key"
	if repo := url.repository(); repo.KeyURL == "" || repo.KeyFile != "" {
		t.Fatalf("URL = %+v", repo)
	}

	none := base
	none.keySource = repoKeySourceNone
	if repo := none.repository(); !repo.InsecureSkipGPGCheck || repo.KeyFile != "" || repo.KeyURL != "" {
		t.Fatalf("sem chave = %+v", repo)
	}
}

func TestRepoDraftAPTAndFlat(t *testing.T) {
	draft := repoDraft{
		name: "Interno", family: core.RepositoryFamilyAPT, url: "https://repo.interno/apt",
		keySource: repoKeySourceNone, aptSuite: "bookworm", aptComponents: "main,contrib",
	}
	repo := draft.repository()
	if repo.Suite != "bookworm" || strings.Join(repo.Components, ",") != "main,contrib" {
		t.Fatalf("APT = %+v", repo)
	}

	draft.aptFlat = true
	repo = draft.repository()
	if !repo.Flat || repo.Suite != "" || len(repo.Components) != 0 {
		t.Fatalf("flat = %+v", repo)
	}
}

func TestRepoDraftRoundTrip(t *testing.T) {
	source := core.PackageRepository{
		Name: "RPM", Family: core.RepositoryFamilyRPM,
		URL: "https://repo.interno/rpm", KeyURL: "https://repo.interno/key",
		CAFile: "/tmp/ca.pem", InsecureSkipTLSVerify: true,
	}
	draft := newRepoDraft(source.Family)
	draft.setRepository(source)
	got := draft.repository()
	if got.Name != source.Name || got.URL != source.URL || got.KeyURL != source.KeyURL {
		t.Fatalf("roundtrip = %+v", got)
	}
	if got.CAFile != source.CAFile || !got.InsecureSkipTLSVerify {
		t.Fatalf("CA/TLS = %+v", got)
	}
}

func TestBuildRepositoriesNormalizes(t *testing.T) {
	m := newRepoTestModel()
	m.repoDrafts = []repoDraft{{
		name: "Interno", family: core.RepositoryFamilyAPT, url: "https://repo.interno/apt",
		keySource: repoKeySourceNone, aptSuite: "bookworm",
	}}
	repos, err := m.buildRepositories()
	if err != nil {
		t.Fatalf("buildRepositories: %v", err)
	}
	if len(repos) != 1 || len(repos[0].Components) != 1 || repos[0].Components[0] != "main" {
		t.Fatalf("repos = %+v", repos)
	}
}

func TestSaveRepoEdit(t *testing.T) {
	m := newRepoTestModel()
	m.repoFamilyEdit = core.RepositoryFamilyAPT
	m.repoEditIdx = -1
	m.repoNameEdit.SetValue("Interno")
	m.repoURLEdit.SetValue("https://repo.interno/apt")
	m.repoKeySourceEdit = repoKeySourceNone
	m.repoAPTSuiteEdit.SetValue("bookworm")
	m.saveRepoEdit()
	if m.screen != screenRepos || len(m.repoDrafts) != 1 {
		t.Fatalf("tela=%v drafts=%d erro=%q", m.screen, len(m.repoDrafts), m.errorMsg)
	}

	m.repoEditIdx = 0
	m.repoNameEdit.SetValue("Interno 2")
	m.saveRepoEdit()
	if len(m.repoDrafts) != 1 || m.repoDrafts[0].name != "Interno 2" {
		t.Fatalf("edição = %+v", m.repoDrafts)
	}
}

func TestSaveRepoEditValidation(t *testing.T) {
	m := newRepoTestModel()
	m.repoFamilyEdit = core.RepositoryFamilyAPT
	m.repoEditIdx = -1
	m.repoNameEdit.SetValue("Interno")
	m.saveRepoEdit()
	if len(m.repoDrafts) != 0 || m.errorMsg == "" {
		t.Fatalf("deveria falhar sem URL: drafts=%d erro=%q", len(m.repoDrafts), m.errorMsg)
	}
}

func TestRemoveRepoDraft(t *testing.T) {
	m := newRepoTestModel()
	m.repoDrafts = []repoDraft{{name: "a"}, {name: "b"}, {name: "c"}}
	m.repoIdx = 2
	m.removeRepoDraft(2)
	if len(m.repoDrafts) != 2 || m.repoIdx != 1 {
		t.Fatalf("drafts=%+v idx=%d", m.repoDrafts, m.repoIdx)
	}
}

func TestRepoFamilyMismatch(t *testing.T) {
	m := newRepoTestModel()
	m.repoDrafts = []repoDraft{{name: "RPM", family: core.RepositoryFamilyRPM, url: "https://repo"}}
	if !m.repoFamilyMismatch() {
		t.Fatal("mismatch não detectado")
	}
}

func TestViewRepos(t *testing.T) {
	m := newRepoTestModel()
	m.repoDrafts = []repoDraft{{
		name: "Interno", family: core.RepositoryFamilyAPT, url: "https://repo.interno/apt",
		keySource: repoKeySourceFile, keyValue: "/tmp/key",
	}}
	view := m.viewRepos()
	if !strings.Contains(view, "Interno") || !strings.Contains(view, "https://repo.interno/apt") {
		t.Fatalf("view inesperada:\n%s", view)
	}
	if !strings.Contains(view, "adicionar") || !strings.Contains(view, "editar") {
		t.Fatalf("atalhos ausentes:\n%s", view)
	}
}
