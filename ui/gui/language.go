//go:build cgo

package gui

import (
	"strings"

	"fyne.io/fyne/v2/widget"

	"opf/internal/i18n"
)

// languageOptions returns the interface-language selector labels in the active
// language. Language names are shown in their own language.
func languageOptions() []string {
	return []string{
		i18n.T("Automático (sistema)"),
		i18n.NativeName(i18n.EN),
		i18n.NativeName(i18n.PTBR),
		i18n.NativeName(i18n.ES),
	}
}

// languageLabel maps a persisted language setting to its selector label.
func languageLabel(setting string) string {
	switch strings.TrimSpace(setting) {
	case i18n.SettingValue(i18n.EN):
		return i18n.NativeName(i18n.EN)
	case i18n.SettingValue(i18n.PTBR):
		return i18n.NativeName(i18n.PTBR)
	case i18n.SettingValue(i18n.ES):
		return i18n.NativeName(i18n.ES)
	default:
		return i18n.T("Automático (sistema)")
	}
}

// languageSetting maps a selector label to the persisted setting value.
func languageSetting(label string) string {
	selected := strings.TrimSpace(label)
	switch selected {
	case i18n.NativeName(i18n.EN), i18n.SettingValue(i18n.EN):
		return i18n.SettingValue(i18n.EN)
	case i18n.NativeName(i18n.PTBR), i18n.SettingValue(i18n.PTBR):
		return i18n.SettingValue(i18n.PTBR)
	case i18n.NativeName(i18n.ES), i18n.SettingValue(i18n.ES):
		return i18n.SettingValue(i18n.ES)
	case i18n.T("Automático (sistema)"), i18n.SettingAuto, "":
		return i18n.SettingAuto
	default:
		return i18n.SettingAuto
	}
}

// syncLanguageSelect rebuilds the selector options in the active language and
// keeps the persisted interface language selected.
func (u *UI) syncLanguageSelect() {
	options := languageOptions()
	placeholder := i18n.T("Selecione um idioma")
	if u.languageSelect == nil {
		u.languageSelect = widget.NewSelect(options, nil)
	} else {
		u.languageSelect.Options = options
		u.languageSelect.Refresh()
	}
	u.languageSelect.PlaceHolder = placeholder
	u.languageSelect.SetSelected(languageLabel(u.settings.Language))
}
