//go:build cgo

package gui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"opf/internal/core"
)

func TestRepoRowAPT(t *testing.T) {
	row := newRepoRow(core.RepositoryFamilyAPT)
	row.name.SetText("Interno")
	row.url.SetText("https://repo.interno/apt")
	row.keySource.SetSelected(repoKeySources[0])
	row.keyValue.SetText("/tmp/key.asc")
	row.aptSuite.SetText("bookworm")
	row.aptComponents.SetText("main, contrib")

	repo := row.repository()
	if repo.Family != core.RepositoryFamilyAPT || repo.Name != "Interno" {
		t.Fatalf("repo = %+v", repo)
	}
	if repo.KeyFile != "/tmp/key.asc" || repo.KeyURL != "" || repo.InsecureSkipGPGCheck {
		t.Fatalf("chave = %+v", repo)
	}
	if repo.Suite != "bookworm" || strings.Join(repo.Components, ",") != "main,contrib" {
		t.Fatalf("APT = %+v", repo)
	}
}

func TestRepoRowKeySources(t *testing.T) {
	row := newRepoRow(core.RepositoryFamilyRPM)
	row.name.SetText("RPM")
	row.url.SetText("https://repo.interno/rpm")
	row.keySource.SetSelected(repoKeySources[2])
	if repo := row.repository(); !repo.InsecureSkipGPGCheck || repo.KeyFile != "" || repo.KeyURL != "" {
		t.Fatalf("sem chave = %+v", repo)
	}

	row.keySource.SetSelected(repoKeySources[1])
	row.keyValue.SetText("https://repo.interno/key.asc")
	if repo := row.repository(); repo.KeyURL == "" || repo.KeyFile != "" || repo.InsecureSkipGPGCheck {
		t.Fatalf("chave URL = %+v", repo)
	}

	row.keySource.SetSelected(repoKeySources[0])
	row.keyValue.SetText("/tmp/key")
	if repo := row.repository(); repo.KeyFile != "/tmp/key" || repo.KeyURL != "" {
		t.Fatalf("chave arquivo = %+v", repo)
	}
}

func TestRepoRowFlatIgnoresAPTFields(t *testing.T) {
	row := newRepoRow(core.RepositoryFamilyAPT)
	row.name.SetText("Flat")
	row.url.SetText("https://repo.interno/flat")
	row.keySource.SetSelected(repoKeySources[2])
	row.aptFlat.SetChecked(true)
	row.aptSuite.SetText("bookworm")
	row.aptComponents.SetText("main")
	repo := row.repository()
	if !repo.Flat || repo.Suite != "" || len(repo.Components) != 0 {
		t.Fatalf("flat = %+v", repo)
	}
}

func TestRepoRowAPKDisablesInsecureTLS(t *testing.T) {
	row := newRepoRow(core.RepositoryFamilyAPK)
	if !row.insecureTLS.Disabled() {
		t.Fatal("insecure TLS deveria estar desabilitado no APK")
	}
}

func TestRepoRowRoundTrip(t *testing.T) {
	source := core.PackageRepository{
		Name: "RPM", Family: core.RepositoryFamilyRPM,
		URL: "https://repo.interno/el9/$basearch", KeyURL: "https://repo.interno/key",
		CAFile: "/tmp/ca.pem", InsecureSkipTLSVerify: true,
	}
	row := newRepoRow(source.Family)
	row.setRepository(source)
	got := row.repository()
	if got.Name != source.Name || got.URL != source.URL || got.KeyURL != source.KeyURL {
		t.Fatalf("roundtrip = %+v", got)
	}
	if got.CAFile != source.CAFile || !got.InsecureSkipTLSVerify {
		t.Fatalf("CA/TLS = %+v", got)
	}
}

func newRepoTestUI(t *testing.T) *UI {
	t.Helper()
	target := widget.NewSelect([]string{"Ubuntu", "Rocky Linux"}, nil)
	target.SetSelected("Ubuntu")
	return &UI{targetSelect: target}
}

func TestAddRemoveRepoRows(t *testing.T) {
	u := newRepoTestUI(t)
	u.addRepoRow()
	u.addRepoRow()
	if len(u.repoRows) != 2 {
		t.Fatalf("repoRows = %d", len(u.repoRows))
	}
	if u.repoRows[0].name.Text != "Repositório 1" || u.repoRows[1].name.Text != "Repositório 2" {
		t.Fatalf("nomes = %q, %q", u.repoRows[0].name.Text, u.repoRows[1].name.Text)
	}
	u.removeRepoRow(0)
	if len(u.repoRows) != 1 || u.repoRows[0].name.Text != "Repositório 2" {
		t.Fatalf("após remover = %+v", u.repoRows)
	}
	u.clearRepoRows()
	if len(u.repoRows) != 0 {
		t.Fatalf("repoRows = %d", len(u.repoRows))
	}
}

func TestBuildRepositoriesValidation(t *testing.T) {
	u := newRepoTestUI(t)
	u.addRepoRow()
	u.repoRows[0].name.SetText("Interno")
	u.repoRows[0].keySource.SetSelected(repoKeySources[2])
	if _, err := u.buildRepositories(); err == nil || !strings.Contains(err.Error(), "URL do repositório") {
		t.Fatalf("erro = %v", err)
	}

	u.repoRows[0].url.SetText("https://repo.interno/apt")
	u.repoRows[0].aptSuite.SetText("bookworm")
	if _, err := u.buildRepositories(); err != nil {
		t.Fatalf("buildRepositories: %v", err)
	}

	u.repoRows[0].family = core.RepositoryFamilyRPM
	if _, err := u.buildRepositories(); err == nil || !strings.Contains(err.Error(), "não corresponde ao alvo") {
		t.Fatalf("erro = %v", err)
	}
}

func TestSetRepositories(t *testing.T) {
	u := newRepoTestUI(t)
	u.setRepositories([]core.PackageRepository{{
		Name: "Interno", Family: core.RepositoryFamilyAPT, URL: "https://repo.interno/apt",
		KeyFile: "/tmp/key", Suite: "bookworm", Components: []string{"main"},
	}})
	if len(u.repoRows) != 1 {
		t.Fatalf("repoRows = %d", len(u.repoRows))
	}
	got := u.repoRows[0].repository()
	if got.Name != "Interno" || got.KeyFile != "/tmp/key" || got.Suite != "bookworm" {
		t.Fatalf("repo = %+v", got)
	}
}

func TestRepoFamilyMismatch(t *testing.T) {
	u := newRepoTestUI(t)
	u.setRepositories([]core.PackageRepository{{
		Name: "RPM", Family: core.RepositoryFamilyRPM, URL: "https://repo.interno/rpm", KeyFile: "/k",
	}})
	if !u.repoFamilyMismatch() {
		t.Fatal("mismatch não detectado")
	}
}
