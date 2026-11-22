package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"opf/internal/app"
	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/i18n"
)

func TestEffectiveVersionReport(t *testing.T) {
	m := &Model{sshEnabled: true, versionReport: string(core.VersionReportAll)}
	if got := m.effectiveVersionReport(); got != core.VersionReportAll {
		t.Fatalf("modo = %q, esperava all", got)
	}
	m.versionReport = string(core.VersionReportNone)
	if got := m.effectiveVersionReport(); got != core.VersionReportNone {
		t.Fatalf("modo = %q, esperava none", got)
	}
	m.versionReport = ""
	if got := m.effectiveVersionReport(); got != core.VersionReportDefault {
		t.Fatalf("modo vazio = %q, esperava default", got)
	}
	m.versionReport = "invalido"
	if got := m.effectiveVersionReport(); got != core.VersionReportDefault {
		t.Fatalf("modo inválido = %q, esperava default", got)
	}
	m.sshEnabled = false
	m.versionReport = string(core.VersionReportAll)
	if got := m.effectiveVersionReport(); got != core.VersionReportNone {
		t.Fatalf("modo sem SSH = %q, esperava none", got)
	}
}

func TestBuildConfigSetsVersionReport(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	dir := t.TempDir()
	m := &Model{
		sshEnabled:    true,
		versionReport: string(core.VersionReportAll),
		settings:      config.Default(),
		pkgInput:      input("vim", ""),
		targetInput:   input("ubuntu", ""),
		versionInput:  input("22.04", ""),
		archInput:     input("amd64", ""),
		methodInput:   input("docker", ""),
		imageInput:    input("", ""),
		wslInput:      input("", ""),
		outInput:      input(filepath.Join(dir, "out"), ""),
		dbInput:       input(filepath.Join(dir, "opf.db"), ""),
		sshHost:       input("destino", ""),
		sshPort:       input("22", ""),
		sshUser:       input("opf", ""),
		sshKey:        input("", ""),
		sshPassword:   passwordInput(""),
		knownHosts:    input("", ""),
		remoteDir:     input("", ""),
		reporter:      &tuiReporter{events: make(chan tea.Msg, 10)},
	}
	cfg, err := m.buildConfig()
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.VersionReport != core.VersionReportAll {
		t.Fatalf("VersionReport = %q, esperava all", cfg.VersionReport)
	}

	m.sshEnabled = false
	cfg, err = m.buildConfig()
	if err != nil {
		t.Fatalf("buildConfig sem SSH: %v", err)
	}
	if cfg.VersionReport != core.VersionReportNone {
		t.Fatalf("VersionReport sem SSH = %q, esperava none", cfg.VersionReport)
	}
}

func TestVersionReportChoiceCyclesWithArrows(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	m := &Model{
		screen:        screenPackages,
		sshEnabled:    true,
		versionReport: string(core.VersionReportDefault),
		pkgInput:      input("", ""),
		sudoPassword:  passwordInput(""),
	}
	m.buildForm()
	index := -1
	for i, item := range m.form {
		if item.choice == &m.versionReport {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("seleção de coleta de versões ausente no formulário")
	}
	m.focus = index
	if _, _ = m.updateForm(tea.KeyMsg{Type: tea.KeyRight}); m.versionReport != string(core.VersionReportNone) {
		t.Fatalf("right = %q, esperava none", m.versionReport)
	}
	if _, _ = m.updateForm(tea.KeyMsg{Type: tea.KeyLeft}); m.versionReport != string(core.VersionReportDefault) {
		t.Fatalf("left = %q, esperava default", m.versionReport)
	}
	if !strings.Contains(m.helpText(), "←→") {
		t.Fatalf("ajuda não documenta as setas: %s", m.helpText())
	}
}

func TestVersionReportDisabledWithoutSSH(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	m := &Model{
		screen:        screenPackages,
		sshEnabled:    false,
		versionReport: string(core.VersionReportAll),
		pkgInput:      input("", ""),
		sudoPassword:  passwordInput(""),
	}
	m.buildForm()
	if got := m.versionReportDisplay(m.versionReport); got != "Nenhum" {
		t.Fatalf("exibição sem SSH = %q, esperava Nenhum", got)
	}
	view := m.View()
	if !strings.Contains(view, "modo forçado para Nenhum") {
		t.Fatalf("aviso de SSH ausente:\n%s", view)
	}
}

func TestViewPackageDiffShowsUpgrades(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	m := &Model{
		screen: screenPackageDiff,
		preview: &app.PackagePreview{Data: core.PackagePreviewData{
			Mode: core.VersionReportDefault,
			Diff: []core.PackageDiff{
				{Name: "vim", From: "8.2", To: "9.0"},
				{Name: "libc6", From: "2.35", To: "2.36", IsDependency: true},
			},
		}},
	}
	view := m.viewPackageDiff()
	for _, want := range []string{"Package diff", "vim", "8.2", "9.0", "libc6", "2.36", "principal", "dependência", "[y/enter]", "[n/esc]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view sem %q:\n%s", want, view)
		}
	}
}

func TestViewPackageDiffLoading(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	m := &Model{screen: screenPackageDiff, previewLoading: true}
	view := m.viewPackageDiff()
	if !strings.Contains(view, "Package diff") || !strings.Contains(view, "Coletando versões instaladas e candidatas...") {
		t.Fatalf("estado de carregamento ausente:\n%s", view)
	}
}

func TestPackageDiffProceedWithoutPreviewReturnsToPackages(t *testing.T) {
	m := &Model{screen: screenPackageDiff, preview: &app.PackagePreview{}}
	if _, _ = m.updatePackageDiff(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); m.screen != screenPackages {
		t.Fatalf("tela = %v, esperava packages", m.screen)
	}
	if m.errorMsg == "" {
		t.Fatal("erro esperado ao prosseguir sem preview")
	}
}

func TestViewResultAbortedNotice(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	m := &Model{screen: screenResult, notice: "Sessão #7 abortada: nenhum pacote foi instalado. O relatório mantém as versões coletadas e a decisão."}
	view := m.View()
	if !strings.Contains(view, "Sessão abortada") || !strings.Contains(view, "Sessão #7 abortada") {
		t.Fatalf("resultado abortado ausente:\n%s", view)
	}
}
