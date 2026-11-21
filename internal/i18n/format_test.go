package i18n

import (
	"regexp"
	"strings"
	"testing"
)

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*([a-zA-Z%])`)

func formatVerbs(value string) string {
	matches := formatVerb.FindAllStringSubmatch(value, -1)
	verbs := make([]string, 0, len(matches))
	for _, match := range matches {
		if match[1] == "%" {
			continue
		}
		verbs = append(verbs, match[1])
	}
	return strings.Join(verbs, "")
}

// TestCatalogFormatVerbs verifies that every translation preserves the format
// verbs of the Portuguese source, preventing fmt.Sprintf panics or broken
// messages at runtime.
func TestCatalogFormatVerbs(t *testing.T) {
	for _, lang := range []Language{EN, ES} {
		for source, translation := range catalogs[lang] {
			if got, want := formatVerbs(translation), formatVerbs(source); got != want {
				t.Errorf("%s: %q -> %q changes format verbs %q to %q", lang, source, translation, want, got)
			}
		}
	}
}
