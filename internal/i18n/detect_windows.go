//go:build windows

package i18n

import "golang.org/x/sys/windows"

var getUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// Detect returns the interface language that matches the operating system UI
// language. Unsupported languages fall back to English.
func Detect() Language {
	result, _, _ := getUserDefaultUILanguage.Call()
	langID := uint16(result)
	// The low 10 bits of a Windows language identifier are the primary
	// language ID (LANG_ENGLISH=0x09, LANG_SPANISH=0x0a, LANG_PORTUGUESE=0x16).
	switch langID & 0x3ff {
	case 0x09:
		return EN
	case 0x16:
		return PTBR
	case 0x0a:
		return ES
	default:
		return EN
	}
}
