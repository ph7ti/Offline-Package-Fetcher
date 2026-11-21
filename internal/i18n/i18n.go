// Package i18n provides the runtime interface language for OPF.
//
// Source strings are written in Brazilian Portuguese (the historical UI
// language). The T function translates them to the active language; the
// Portuguese source is returned unchanged for pt-BR, while English and Spanish
// use the catalogs embedded from catalog/<domain>/<language>.json.
//
// English is the fallback language: when the operating system language is not
// English, Portuguese or Spanish, the interface starts in English.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

// Language is a supported interface language.
type Language string

const (
	// EN is English, the default/fallback language.
	EN Language = "en"
	// PTBR is Brazilian Portuguese, the source language of the UI strings.
	PTBR Language = "pt-BR"
	// ES is Spanish.
	ES Language = "es"
)

// SettingAuto asks OPF to follow the operating system language.
const SettingAuto = "auto"

// Supported returns the supported languages in display order.
func Supported() []Language { return []Language{EN, PTBR, ES} }

// NativeName returns the language name in its own language.
func NativeName(l Language) string {
	switch l {
	case PTBR:
		return "Português (BR)"
	case ES:
		return "Español"
	default:
		return "English"
	}
}

// SettingValue converts a language to the persisted setting value.
func SettingValue(l Language) string { return string(l) }

var (
	mu         sync.RWMutex
	current    = EN
	catalogs   = map[Language]map[string]string{}
	catalogErr error
)

//go:embed catalog
var catalogFS embed.FS

func init() {
	catalogErr = loadCatalogs()
	// Follow the operating system (or OPF_LANG) until an explicit setting is
	// applied by the CLI, TUI or GUI.
	current = Resolve("")
}

func loadCatalogs() error {
	return fs.WalkDir(catalogFS, "catalog", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path.Ext(filePath) != ".json" {
			return nil
		}
		lang := Language(strings.TrimSuffix(path.Base(filePath), ".json"))
		data, readErr := catalogFS.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		var messages map[string]string
		if unmarshalErr := json.Unmarshal(data, &messages); unmarshalErr != nil {
			return fmt.Errorf("catalog %s: %w", filePath, unmarshalErr)
		}
		if catalogs[lang] == nil {
			catalogs[lang] = map[string]string{}
		}
		for source, translation := range messages {
			catalogs[lang][source] = translation
		}
		return nil
	})
}

// CatalogError returns the error found while loading embedded catalogs, if any.
func CatalogError() error {
	mu.RLock()
	defer mu.RUnlock()
	return catalogErr
}

// Resolve converts a persisted setting ("auto", "en", "pt-BR", "es") to a
// concrete language. Unknown or empty values follow OPF_LANG when set, then
// the operating system.
func Resolve(setting string) Language {
	if l, ok := parseLanguage(setting); ok && l != "" {
		return l
	}
	if env := strings.TrimSpace(os.Getenv("OPF_LANG")); env != "" {
		if l, ok := parseLanguage(env); ok && l != "" {
			return l
		}
	}
	return Detect()
}

func parseLanguage(value string) (Language, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(EN):
		return EN, true
	case strings.ToLower(string(PTBR)), "pt", "pt_br":
		return PTBR, true
	case string(ES):
		return ES, true
	case "", SettingAuto:
		return "", true
	default:
		return "", false
	}
}

// Set activates a concrete language.
func Set(l Language) {
	mu.Lock()
	defer mu.Unlock()
	if l != EN && l != PTBR && l != ES {
		l = EN
	}
	current = l
}

// SetFromSetting resolves and activates a persisted setting.
func SetFromSetting(setting string) Language {
	l := Resolve(setting)
	Set(l)
	return l
}

// Current returns the active language.
func Current() Language {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T translates a Brazilian Portuguese source string to the active language.
// When format arguments are supplied the translated string is formatted with
// fmt.Sprintf. Missing translations fall back to the Portuguese source.
func T(source string, args ...any) string {
	mu.RLock()
	lang := current
	messages := catalogs[lang]
	mu.RUnlock()

	translated := source
	if lang != PTBR && messages != nil {
		if value, ok := messages[source]; ok && value != "" {
			translated = value
		}
	}
	if len(args) == 0 {
		return translated
	}
	return fmt.Sprintf(translated, args...)
}

// HasTranslation reports whether the active language has a translation for
// source. Portuguese is the source language and always reports true.
func HasTranslation(source string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == PTBR {
		return true
	}
	_, ok := catalogs[current][source]
	return ok
}

// HasTranslationFor reports whether lang has a translation for source.
func HasTranslationFor(lang Language, source string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if lang == PTBR {
		return true
	}
	_, ok := catalogs[lang][source]
	return ok
}

// CatalogLanguages returns the languages that have an embedded catalog.
func CatalogLanguages() []Language {
	mu.RLock()
	defer mu.RUnlock()
	langs := make([]Language, 0, len(catalogs))
	for l := range catalogs {
		langs = append(langs, l)
	}
	return langs
}
