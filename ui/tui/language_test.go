package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/config"
	"opf/internal/i18n"
)

func TestNormalizeLanguageSetting(t *testing.T) {
	cases := map[string]string{
		"":      "auto",
		"auto":  "auto",
		"en":    "en",
		"EN":    "en",
		"pt":    "pt-BR",
		"pt-br": "pt-BR",
		"pt_BR": "pt-BR",
		"es":    "es",
	}
	for input, want := range cases {
		got, ok := normalizeLanguageSetting(input)
		if !ok || got != want {
			t.Errorf("normalizeLanguageSetting(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	if _, ok := normalizeLanguageSetting("fr"); ok {
		t.Error("normalizeLanguageSetting must reject unsupported languages")
	}
}

func TestSaveSettingsLanguagePersistsAndRerenders(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	settingsPath := filepath.Join(t.TempDir(), "config.json")
	m := &Model{
		screen:           screenSettings,
		settingsPath:     settingsPath,
		settings:         config.Default(),
		settingsTemp:     input("", ""),
		settingsTimeout:  input("600", ""),
		settingsLanguage: input("es", ""),
		syslogHost:       input("", ""),
		syslogPort:       input("514", ""),
		syslogProto:      input("udp", ""),
		syslogTag:        input("opf", ""),
	}
	m.buildForm()
	m.saveSettings()
	if m.errorMsg == "" {
		t.Fatal("saveSettings did not report a result")
	}
	if i18n.Current() != i18n.ES {
		t.Fatalf("active language = %q, want es", i18n.Current())
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatalf("load saved settings: %v", err)
	}
	if loaded.Language != "es" {
		t.Fatalf("persisted language = %q, want es", loaded.Language)
	}
	view := m.View()
	if !strings.Contains(view, "Configuraciones Globales") || !strings.Contains(view, "Idioma de la interfaz") {
		t.Fatalf("settings screen did not re-render in Spanish:\n%s", view)
	}

	m.settingsLanguage.SetValue("fr")
	m.saveSettings()
	if !strings.Contains(m.errorMsg, "idioma") {
		t.Fatalf("invalid language should be rejected, error = %q", m.errorMsg)
	}
}

func TestSettingsScreenRendersEnglish(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.EN)

	m := &Model{
		screen:           screenSettings,
		settings:         config.Default(),
		settingsLanguage: input("en", ""),
	}
	m.buildForm()
	view := m.View()
	for _, want := range []string{"Global Settings", "Interface language", "Current language: auto (English)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings screen missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Configurações") {
		t.Fatalf("settings screen still shows Portuguese in English mode:\n%s", view)
	}
}
