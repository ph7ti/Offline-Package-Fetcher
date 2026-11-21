//go:build !windows

package i18n

import (
	"os"
	"strings"
)

// Detect returns the interface language that matches the operating system
// locale environment (LC_ALL, LC_MESSAGES, LANG). Unsupported locales fall
// back to English.
func Detect() Language {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if value == "" || value == "c" || value == "posix" {
			continue
		}
		value = strings.NewReplacer("_", "-", ".", "-").Replace(value)
		switch {
		case strings.HasPrefix(value, "pt"):
			return PTBR
		case strings.HasPrefix(value, "es"):
			return ES
		case strings.HasPrefix(value, "en"):
			return EN
		}
	}
	return EN
}
