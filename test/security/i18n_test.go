package security

import (
	"strings"
	"testing"

	"opf/internal/config"
	"opf/internal/i18n"
)

// TestLanguageSettingRejectsInjection ensures the persisted interface language
// only accepts the documented values and cannot smuggle paths or control
// characters into config.json.
func TestLanguageSettingRejectsInjection(t *testing.T) {
	for _, language := range []string{
		"../../etc/passwd",
		"en\nInjected: true",
		"pt-BR;rm -rf /",
		"en\x00",
		"<script>",
	} {
		settings := config.Default()
		settings.Language = language
		if err := settings.Validate(); err == nil {
			t.Errorf("language %q accepted by settings validation", language)
		}
	}
	for _, language := range []string{"", "auto", "en", "pt-BR", "es"} {
		settings := config.Default()
		settings.Language = language
		if err := settings.Validate(); err != nil {
			t.Errorf("language %q rejected: %v", language, err)
		}
	}
}

// TestLanguageResolutionIsSafe asserts unknown or hostile language values
// never escape the supported set and never panic.
func TestLanguageResolutionIsSafe(t *testing.T) {
	supported := map[i18n.Language]bool{i18n.EN: true, i18n.PTBR: true, i18n.ES: true}
	for _, value := range []string{
		"", "auto", "en", "pt-BR", "es", "pt_BR", "pt", "fr", "de-DE",
		"../../etc/passwd", "en;rm -rf /", strings.Repeat("a", 4096), "en\x00",
	} {
		language := i18n.Resolve(value)
		if !supported[language] {
			t.Errorf("Resolve(%q) returned unsupported language %q", value, language)
		}
	}
}

// TestTranslationFormattingHandlesUserData asserts translated strings with
// formatting verbs remain safe when arguments contain hostile text.
func TestTranslationFormattingHandlesUserData(t *testing.T) {
	for _, language := range i18n.Supported() {
		i18n.Set(language)
		got := i18n.T("Nova sessão %d", 7)
		if !strings.Contains(got, "7") {
			t.Errorf("%s: formatting lost the argument: %q", language, got)
		}
		hostile := i18n.T("%s", "$(id);\n<xml>")
		if !strings.Contains(hostile, "$(id)") {
			t.Errorf("%s: formatting must keep user data as data, got %q", language, hostile)
		}
	}
	i18n.Set(i18n.PTBR)
}
