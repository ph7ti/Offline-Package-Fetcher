//go:build cgo

package gui

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Custom semantic colors used by the desktop shell and group panels.
const (
	colorPageBackground fyne.ThemeColorName = "opfPageBackground"
	colorCardBackground fyne.ThemeColorName = "opfCardBackground"
	colorMutedText      fyne.ThemeColorName = "opfMutedText"
	colorBorder         fyne.ThemeColorName = "opfBorder"
	colorSoftPrimary    fyne.ThemeColorName = "opfSoftPrimary"
	colorSoftSecondary  fyne.ThemeColorName = "opfSoftSecondary"
	colorSoftSuccess    fyne.ThemeColorName = "opfSoftSuccess"
	colorSoftWarning    fyne.ThemeColorName = "opfSoftWarning"
	colorSoftError      fyne.ThemeColorName = "opfSoftError"
	colorSecondary      fyne.ThemeColorName = "opfSecondary"
	colorOnHeader       fyne.ThemeColorName = "opfOnHeader"
	colorMutedOnHeader  fyne.ThemeColorName = "opfMutedOnHeader"
)

// opfTheme keeps Fyne's fonts and icons while applying the OPF desktop palette.
type opfTheme struct {
	fallback fyne.Theme
	forced   bool
	variant  fyne.ThemeVariant
}

func newOPFTheme() fyne.Theme {
	return &opfTheme{fallback: theme.DefaultTheme()}
}

func newOPFThemeVariant(variant fyne.ThemeVariant) fyne.Theme {
	return &opfTheme{fallback: theme.DefaultTheme(), forced: true, variant: variant}
}

func applyThemeMode(a fyne.App, mode string) {
	switch mode {
	case "light":
		a.Settings().SetTheme(newOPFThemeVariant(theme.VariantLight))
	case "dark":
		a.Settings().SetTheme(newOPFThemeVariant(theme.VariantDark))
	default:
		a.Settings().SetTheme(newOPFTheme())
	}
}

func (t *opfTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.forced {
		variant = t.variant
	}
	palette := opfLight
	if variant == theme.VariantDark {
		palette = opfDark
	}
	if value, ok := palette[name]; ok {
		return value
	}
	return t.fallback.Color(name, variant)
}

func (t *opfTheme) Font(style fyne.TextStyle) fyne.Resource {
	return t.fallback.Font(style)
}

func (t *opfTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.fallback.Icon(name)
}

func (t *opfTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 6
	case theme.SizeNameLineSpacing:
		return 3
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius:
		return 5
	case theme.SizeNameButtonRadius:
		return 5
	case theme.SizeNameCardRadius:
		return 6
	case theme.SizeNameDialogRadius:
		return 8
	case theme.SizeNamePopupRadius:
		return 6
	case theme.SizeNameMenuRadius:
		return 4
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 4
	default:
		return t.fallback.Size(name)
	}
}

var opfLight = map[fyne.ThemeColorName]color.Color{
	colorPageBackground: hex("#F2F4F6"),
	colorCardBackground: hex("#FFFFFF"),
	colorMutedText:      hex("#5D6874"),
	colorBorder:         hex("#C9CED3"),
	colorSoftPrimary:    hex("#FFF1E2"),
	colorSoftSecondary:  hex("#E7F4FC"),
	colorSoftSuccess:    hex("#E8F6EF"),
	colorSoftWarning:    hex("#FFF4E5"),
	colorSoftError:      hex("#FDECEC"),
	colorSecondary:      hex("#2191D5"),
	colorOnHeader:       hex("#FFFFFF"),
	colorMutedOnHeader:  hex("#C9CED3"),

	theme.ColorNameBackground:          hex("#FFFFFF"),
	theme.ColorNameForeground:          hex("#1B222D"),
	theme.ColorNameForegroundOnPrimary: hex("#1B222D"),
	theme.ColorNameForegroundOnError:   hex("#FFFFFF"),
	theme.ColorNameForegroundOnSuccess: hex("#FFFFFF"),
	theme.ColorNameForegroundOnWarning: hex("#1B222D"),
	theme.ColorNamePrimary:             hex("#FB9632"),
	theme.ColorNameButton:              hex("#E9EDF1"),
	theme.ColorNameDisabledButton:      hex("#F1F3F5"),
	theme.ColorNameDisabled:            hex("#98A2AC"),
	theme.ColorNameError:               hex("#C0392B"),
	theme.ColorNameFocus:               hex("#FDCFA0"),
	theme.ColorNameHeaderBackground:    hex("#1B222D"),
	theme.ColorNameHover:               hex("#FDEBD9"),
	theme.ColorNameHyperlink:           hex("#1B73A8"),
	theme.ColorNameInputBackground:     hex("#FFFFFF"),
	theme.ColorNameInputBorder:         hex("#AEB6BF"),
	theme.ColorNameMenuBackground:      hex("#FFFFFF"),
	theme.ColorNameOverlayBackground:   hex("#FFFFFF"),
	theme.ColorNamePlaceHolder:         hex("#8C97A2"),
	theme.ColorNamePressed:             hex("#FBD5AE"),
	theme.ColorNameScrollBar:           hex("#B0BAC4"),
	theme.ColorNameScrollBarBackground: hex("#EEF1F4"),
	theme.ColorNameSelection:           hex("#FDD9B2"),
	theme.ColorNameSeparator:           hex("#DDE2E7"),
	theme.ColorNameShadow:              rgba("#1B222D", 22),
	theme.ColorNameSuccess:             hex("#1F8A5B"),
	theme.ColorNameWarning:             hex("#B26A00"),
}

var opfDark = map[fyne.ThemeColorName]color.Color{
	colorPageBackground: hex("#141A22"),
	colorCardBackground: hex("#1F2732"),
	colorMutedText:      hex("#A7B0BA"),
	colorBorder:         hex("#38424F"),
	colorSoftPrimary:    hex("#3A2A1B"),
	colorSoftSecondary:  hex("#16364A"),
	colorSoftSuccess:    hex("#123629"),
	colorSoftWarning:    hex("#3A2A12"),
	colorSoftError:      hex("#3A1D1D"),
	colorSecondary:      hex("#4FB3E8"),
	colorOnHeader:       hex("#FFFFFF"),
	colorMutedOnHeader:  hex("#C9CED3"),

	theme.ColorNameBackground:          hex("#1F2732"),
	theme.ColorNameForeground:          hex("#E6E9ED"),
	theme.ColorNameForegroundOnPrimary: hex("#1B222D"),
	theme.ColorNameForegroundOnError:   hex("#FFFFFF"),
	theme.ColorNameForegroundOnSuccess: hex("#FFFFFF"),
	theme.ColorNameForegroundOnWarning: hex("#1B222D"),
	theme.ColorNamePrimary:             hex("#FB9632"),
	theme.ColorNameButton:              hex("#2A3440"),
	theme.ColorNameDisabledButton:      hex("#242C36"),
	theme.ColorNameDisabled:            hex("#6F7A86"),
	theme.ColorNameError:               hex("#F28B82"),
	theme.ColorNameFocus:               hex("#8A5A25"),
	theme.ColorNameHeaderBackground:    hex("#10151C"),
	theme.ColorNameHover:               hex("#3A3024"),
	theme.ColorNameHyperlink:           hex("#6FC3ED"),
	theme.ColorNameInputBackground:     hex("#161D26"),
	theme.ColorNameInputBorder:         hex("#46515F"),
	theme.ColorNameMenuBackground:      hex("#1F2732"),
	theme.ColorNameOverlayBackground:   hex("#1F2732"),
	theme.ColorNamePlaceHolder:         hex("#7C8793"),
	theme.ColorNamePressed:             hex("#4A3A26"),
	theme.ColorNameScrollBar:           hex("#5A6674"),
	theme.ColorNameScrollBarBackground: hex("#141A22"),
	theme.ColorNameSelection:           hex("#6B451C"),
	theme.ColorNameSeparator:           hex("#38424F"),
	theme.ColorNameShadow:              rgba("#000000", 120),
	theme.ColorNameSuccess:             hex("#5FD3A0"),
	theme.ColorNameWarning:             hex("#F5B55F"),
}

func hex(value string) color.Color {
	if len(value) == 7 && value[0] == '#' {
		if parsed, err := strconv.ParseUint(value[1:], 16, 32); err == nil {
			return color.NRGBA{
				R: uint8(parsed >> 16),
				G: uint8(parsed >> 8),
				B: uint8(parsed),
				A: 0xFF,
			}
		}
	}
	return color.NRGBA{R: 0x1B, G: 0x22, B: 0x2D, A: 0xFF}
}

func rgba(value string, alpha uint8) color.Color {
	base := hex(value).(color.NRGBA)
	base.A = alpha
	return base
}
