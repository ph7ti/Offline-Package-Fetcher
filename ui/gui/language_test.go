//go:build cgo

package gui

import (
	"testing"

	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/i18n"
)

func TestLanguageSelectorMapping(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)

	settings := []string{i18n.SettingAuto, i18n.SettingValue(i18n.EN), i18n.SettingValue(i18n.PTBR), i18n.SettingValue(i18n.ES)}
	for _, active := range []i18n.Language{i18n.PTBR, i18n.EN, i18n.ES} {
		i18n.Set(active)
		for _, setting := range settings {
			label := languageLabel(setting)
			if got := languageSetting(label); got != setting {
				t.Errorf("[%s] languageSetting(languageLabel(%q)) = %q, want %q", active, setting, got, setting)
			}
		}
		if got := languageLabel(""); got != i18n.T("Automático (sistema)") {
			t.Errorf("[%s] languageLabel(\"\") = %q", active, got)
		}
		if got := languageSetting("rótulo desconhecido"); got != i18n.SettingAuto {
			t.Errorf("[%s] unknown label = %q, want auto", active, got)
		}
	}
}

func TestSyncLanguageSelectRebuildsOptions(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)

	ui := &UI{settings: config.Settings{Language: i18n.SettingValue(i18n.EN)}}
	i18n.Set(i18n.PTBR)
	ui.syncLanguageSelect()
	if ui.languageSelect.Selected != "English" {
		t.Fatalf("seleção = %q, want English", ui.languageSelect.Selected)
	}

	i18n.Set(i18n.ES)
	ui.settings.Language = i18n.SettingValue(i18n.ES)
	ui.syncLanguageSelect()
	if ui.languageSelect.Selected != "Español" {
		t.Fatalf("seleção = %q, want Español", ui.languageSelect.Selected)
	}
	foundAuto := false
	for _, option := range ui.languageSelect.Options {
		if option == i18n.T("Automático (sistema)") {
			foundAuto = true
		}
	}
	if !foundAuto {
		t.Fatalf("opções sem o rótulo automático traduzido: %v", ui.languageSelect.Options)
	}
}

func TestTranslatedSelectorsKeepSemantics(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.EN)

	row := newJumpRow()
	if row.auth.Selected != "Password" {
		t.Fatalf("auth selecionado = %q, want Password", row.auth.Selected)
	}
	if row.authMode() != "password" {
		t.Fatalf("authMode() = %q, want password", row.authMode())
	}
	row.auth.SetSelected("Key + password")
	if row.authMode() != "key+password" {
		t.Fatalf("authMode() = %q, want key+password", row.authMode())
	}

	repo := newRepoRow(core.RepositoryFamilyAPT)
	if repo.keySource.Selected != "File" {
		t.Fatalf("origem da chave = %q, want File", repo.keySource.Selected)
	}
	repo.keySource.SetSelected("No key (insecure)")
	if repoKeySourceIndex(repo.keySource.Selected) != 2 {
		t.Fatalf("índice da origem da chave = %d, want 2", repoKeySourceIndex(repo.keySource.Selected))
	}
	repo.url.SetText("https://repo.interno/apt")
	if got := repo.repository(); !got.InsecureSkipGPGCheck {
		t.Fatalf("repositório sem chave = %+v", got)
	}

	if got := methodMode("Automatic"); got != "auto" {
		t.Fatalf("methodMode(Automatic) = %q, want auto", got)
	}
	if got := methodLabel("auto"); got != "Automatic" {
		t.Fatalf("methodLabel(auto) = %q, want Automatic", got)
	}
}
