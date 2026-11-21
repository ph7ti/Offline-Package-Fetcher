package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguageDefaultsToAuto(t *testing.T) {
	if got := Default().Language; got != "auto" {
		t.Fatalf("Default().Language = %q, want auto", got)
	}
}

func TestLanguageRoundTrip(t *testing.T) {
	for _, language := range []string{"auto", "en", "pt-BR", "es"} {
		settings := Default()
		settings.Language = language
		path := filepath.Join(t.TempDir(), "config.json")
		if err := Save(path, settings); err != nil {
			t.Fatalf("Save(%q): %v", language, err)
		}
		loaded, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", language, err)
		}
		if loaded.Language != language {
			t.Fatalf("roundtrip language = %q, want %q", loaded.Language, language)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"language": "`+language+`"`) {
			t.Fatalf("language not persisted as JSON: %s", data)
		}
	}
}

func TestLanguageValidation(t *testing.T) {
	settings := Default()
	settings.Language = "fr"
	if err := settings.Validate(); err == nil {
		t.Fatal("unsupported language must be rejected")
	}
	settings.Language = ""
	if err := settings.Validate(); err != nil {
		t.Fatalf("empty language must be accepted as auto: %v", err)
	}
}
