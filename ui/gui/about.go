//go:build cgo

package gui

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"opf/internal/i18n"
	"opf/internal/version"
)

type aboutDetail struct {
	Label string
	Value string
}

// aboutDetails is kept separate from the dialog so the metadata can be tested
// without opening a Fyne window.
func aboutDetails() []aboutDetail {
	info := version.Current()
	return []aboutDetail{
		{Label: i18n.T("Nome"), Value: "Offline Package Fetcher (OPF)"},
		{Label: i18n.T("Versão"), Value: info.Version},
		{Label: i18n.T("Build / commit"), Value: info.Commit},
		{Label: i18n.T("Data de lançamento"), Value: info.ReleaseDate},
		{Label: i18n.T("Desenvolvedor"), Value: "Raphael Pereira"},
		{Label: i18n.T("Licença"), Value: "GPL-3.0"},
	}
}

func (u *UI) showAbout() {
	logo := canvas.NewImageFromResource(appIcon)
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(96, 96))

	name := widget.NewLabelWithStyle("Offline Package Fetcher (OPF)", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabelWithStyle(i18n.T("Gerenciador de pacotes offline"), fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	rows := make([]fyne.CanvasObject, 0, len(aboutDetails())*2)
	for _, detail := range aboutDetails() {
		rows = append(rows,
			widget.NewLabelWithStyle(detail.Label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewLabel(detail.Value),
		)
	}
	details := container.NewGridWithColumns(2, rows...)

	github := widget.NewHyperlink("github.com/ph7ti", parseURL("https://github.com/ph7ti"))
	email := widget.NewHyperlink("raphaelmatheus.exp@gmail.com", parseURL("mailto:raphaelmatheus.exp@gmail.com"))
	linkedin := widget.NewHyperlink("linkedin.com/in/raphaelmpereira", parseURL("https://www.linkedin.com/in/raphaelmpereira/"))
	links := container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("Links"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		github,
		email,
		linkedin,
	)

	license := widget.NewLabelWithStyle("GPL-3.0", fyne.TextAlignCenter, fyne.TextStyle{})
	content := container.NewVBox(
		container.NewCenter(logo),
		name,
		subtitle,
		widget.NewSeparator(),
		details,
		widget.NewSeparator(),
		links,
		widget.NewSeparator(),
		license,
		widget.NewLabelWithStyle(i18n.T("Software livre sob os termos da licença GPL-3.0."), fyne.TextAlignCenter, fyne.TextStyle{}),
	)

	d := dialog.NewCustom(i18n.T("Sobre o OPF"), i18n.T("Fechar"), content, u.window)
	d.Resize(fyne.NewSize(520, 620))
	d.Show()
}

func parseURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		return &url.URL{}
	}
	return parsed
}
