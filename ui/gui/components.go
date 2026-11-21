//go:build cgo

package gui

import (
	"context"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"opf/internal/core"
)

type uiTone int

const (
	toneInfo uiTone = iota
	toneSuccess
	toneWarning
	toneError
)

func (u *UI) showScreen(title string, content fyne.CanvasObject) {
	u.screenTitle = title
	eyebrow, subtitle := pageCopy(title)
	header := container.NewBorder(nil, nil, hSpace(16), hSpace(16), container.NewVBox(
		textLine(strings.ToUpper(eyebrow), 9, true, theme.Color(colorSecondary)),
		vSpace(2),
		textLine(title, 20, true, theme.Color(theme.ColorNameForeground)),
		vSpace(2),
		u.muted(subtitle, theme.SizeNameCaptionText),
	))

	scrollContent := container.NewVBox(content, vSpace(16))
	scroll := container.NewVScroll(container.NewBorder(nil, nil, hSpace(16), hSpace(16), scrollContent))
	body := container.NewBorder(container.NewVBox(vSpace(8), header, vSpace(6), widget.NewSeparator()), nil, nil, nil, scroll)
	page := container.NewBorder(u.appBar(), u.statusBar(), nil, nil, body)
	background := canvas.NewRectangle(theme.Color(colorPageBackground))
	u.window.SetContent(container.NewStack(background, page))
}

func pageCopy(title string) (string, string) {
	switch title {
	case "Início":
		return "Painel de controle", "Atalhos, indicadores e sessões recentes do OPF."
	case "Configuração da Sessão":
		return "Passo 01 • destino", "Defina o sistema alvo, o método de download e o diretório de saída."
	case "Conexão SSH e Jump Hosts":
		return "Passo 02 • acesso", "Conecte ao destino com detecção automática e verificação estrita de host key."
	case "Pacotes e Pré-check":
		return "Passo 03 • carga", "Informe os pacotes e escolha como a instalação será tratada no destino."
	case "Pipeline":
		return "Em execução", "Acompanhe o download, a validação e a transferência sem perder o contexto."
	case "Resultado":
		return "Fechamento", "Resumo da operação, destinos usados e próximos passos."
	case "Relatório da Sessão":
		return "Auditoria", "Documento completo da sessão renderizado a partir do banco local."
	case "Histórico":
		return "Registros", "Consulte, retome ou audite sessões anteriores."
	case "Configurações Globais":
		return "Preferências", "Banco de dados, armazenamento temporário e integração com Syslog."
	default:
		return "OPF", "Offline Package Fetcher"
	}
}

func (u *UI) appBar() fyne.CanvasObject {
	barBackground := canvas.NewRectangle(theme.Color(theme.ColorNameHeaderBackground))
	barBackground.SetMinSize(fyne.NewSize(0, 52))

	logo := canvas.NewImageFromResource(appIcon)
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(32, 32))
	brand := container.NewVBox(
		textLine("Offline Package Fetcher", 14, true, theme.Color(colorOnHeader)),
		textLine("Gerenciador de pacotes offline", 9, false, theme.Color(colorMutedOnHeader)),
	)
	left := container.NewHBox(logo, hSpace(10), brand)

	homeAction := widget.NewToolbarAction(theme.NewColoredResource(theme.HomeIcon(), colorOnHeader), func() {
		if !u.running {
			u.showWelcome()
		}
	})
	historyAction := widget.NewToolbarAction(theme.NewColoredResource(theme.HistoryIcon(), colorOnHeader), func() {
		if !u.running {
			u.showHistory()
		}
	})
	settingsAction := widget.NewToolbarAction(theme.NewColoredResource(theme.SettingsIcon(), colorOnHeader), func() {
		if !u.running {
			u.showSettings()
		}
	})
	if u.running {
		homeAction.Disable()
		historyAction.Disable()
		settingsAction.Disable()
	}
	nav := widget.NewToolbar(homeAction, historyAction, settingsAction)
	themeButton := widget.NewButtonWithIcon("Tema", theme.ColorPaletteIcon(), u.toggleTheme)
	if u.running {
		themeButton.Disable()
	}
	right := container.NewHBox(nav, hSpace(8), themeButton)

	bar := container.NewBorder(nil, nil, hSpace(16), hSpace(16),
		container.NewHBox(left, layout.NewSpacer(), right))
	padded := container.NewBorder(vSpace(8), vSpace(8), nil, nil, bar)
	return container.NewStack(barBackground, padded)
}

func (u *UI) statusBar() fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Color(colorPageBackground))
	left := textLine(u.appStatusText(), 10, false, u.appStatusColor())
	db := textLine("Banco: "+filepath.Base(u.dbEntry.Text), 10, false, theme.Color(colorMutedText))
	right := textLine("Tema: "+u.themeLabel(), 10, false, theme.Color(colorMutedText))
	row := container.NewHBox(left, hSpace(16), db, layout.NewSpacer(), right)
	padded := container.NewBorder(vSpace(3), vSpace(3), hSpace(16), hSpace(16), row)
	content := container.NewBorder(widget.NewSeparator(), nil, nil, nil, padded)
	return container.NewStack(background, content)
}

func (u *UI) themeLabel() string {
	switch u.themeMode {
	case "light":
		return "claro"
	case "dark":
		return "escuro"
	default:
		return "sistema"
	}
}

func (u *UI) toggleTheme() {
	current := u.fyneApp.Settings().ThemeVariant()
	switch u.themeMode {
	case "light":
		current = theme.VariantLight
	case "dark":
		current = theme.VariantDark
	}
	next := theme.VariantDark
	u.themeMode = "dark"
	if current == theme.VariantDark {
		next = theme.VariantLight
		u.themeMode = "light"
	}
	u.fyneApp.Preferences().SetString("theme", u.themeMode)
	u.fyneApp.Settings().SetTheme(newOPFThemeVariant(next))
	u.refreshCurrentScreen()
}

func (u *UI) refreshCurrentScreen() {
	switch u.screenTitle {
	case "Início":
		u.showWelcome()
	case "Configuração da Sessão":
		u.showConfig()
	case "Conexão SSH e Jump Hosts":
		u.showSSH()
	case "Pacotes e Pré-check":
		u.showPackages()
	case "Resultado":
		u.showResult()
	case "Relatório da Sessão":
		u.showReport(u.reportSessionID)
	case "Histórico":
		u.showHistory()
	case "Configurações Globais":
		u.showSettings()
	default:
		u.showWelcome()
	}
}

func (u *UI) appStatusColor() color.Color {
	switch {
	case u.running:
		return theme.Color(theme.ColorNameWarning)
	case u.store == nil:
		return theme.Color(theme.ColorNameError)
	default:
		return theme.Color(theme.ColorNameSuccess)
	}
}

func (u *UI) appStatusText() string {
	switch {
	case u.running:
		return "●  pipeline em execução"
	case u.store == nil:
		return "●  SQLite indisponível"
	case u.sessionID > 0:
		return fmt.Sprintf("●  sessão #%d", u.sessionID)
	default:
		return "●  pronto para operar"
	}
}

func (u *UI) sectionCard(title, subtitle string, content fyne.CanvasObject) fyne.CanvasObject {
	headerObjects := []fyne.CanvasObject{
		textLine(title, 15, true, theme.Color(theme.ColorNameForeground)),
	}
	if subtitle != "" {
		headerObjects = append(headerObjects, vSpace(1), u.muted(subtitle, theme.SizeNameCaptionText))
	}
	inner := container.NewVBox(
		container.NewVBox(headerObjects...),
		vSpace(7),
		widget.NewSeparator(),
		vSpace(8),
		content,
	)
	return u.cardShell(theme.Color(colorCardBackground), theme.Color(colorBorder), 1, inner)
}

func (u *UI) cardShell(fill, border color.Color, borderWidth float32, content fyne.CanvasObject) fyne.CanvasObject {
	background := canvas.NewRectangle(fill)
	background.CornerRadius = 6
	if borderWidth > 0 {
		background.StrokeColor = border
		background.StrokeWidth = borderWidth
	}
	padded := container.NewBorder(vSpace(12), vSpace(12), hSpace(14), hSpace(14), content)
	return container.NewStack(background, padded)
}

func (u *UI) muted(text string, size fyne.ThemeSizeName) *widget.RichText {
	rich := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		ColorName: colorMutedText,
		SizeName:  size,
	}})
	rich.Wrapping = fyne.TextWrapWord
	return rich
}

func (u *UI) primaryButton(label string, icon fyne.Resource, tapped func()) *widget.Button {
	button := widget.NewButtonWithIcon(label, icon, tapped)
	button.Importance = widget.HighImportance
	return button
}

func (u *UI) secondaryButton(label string, icon fyne.Resource, tapped func()) *widget.Button {
	return widget.NewButtonWithIcon(label, icon, tapped)
}

func (u *UI) ghostButton(label string, icon fyne.Resource, tapped func()) *widget.Button {
	button := widget.NewButtonWithIcon(label, icon, tapped)
	button.Importance = widget.LowImportance
	return button
}

func (u *UI) dangerButton(label string, icon fyne.Resource, tapped func()) *widget.Button {
	button := widget.NewButtonWithIcon(label, icon, tapped)
	button.Importance = widget.DangerImportance
	return button
}

func (u *UI) nav(back func(), nextLabel string, next func()) fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, 0, 3)
	if back != nil {
		objects = append(objects, u.ghostButton("Voltar", theme.NavigateBackIcon(), back))
	}
	objects = append(objects, layout.NewSpacer())
	if next != nil {
		objects = append(objects, u.primaryButton(nextLabel, theme.NavigateNextIcon(), next))
	}
	return container.NewHBox(objects...)
}

func (u *UI) banner(message string, tone uiTone) fyne.CanvasObject {
	bgName, fgName, icon := colorSoftSecondary, colorSecondary, theme.InfoIcon()
	switch tone {
	case toneSuccess:
		bgName, fgName, icon = colorSoftSuccess, theme.ColorNameSuccess, theme.ConfirmIcon()
	case toneWarning:
		bgName, fgName, icon = colorSoftWarning, theme.ColorNameWarning, theme.WarningIcon()
	case toneError:
		bgName, fgName, icon = colorSoftError, theme.ColorNameError, theme.ErrorIcon()
	}
	text := widget.NewRichText(&widget.TextSegment{Text: message, Style: widget.RichTextStyle{
		ColorName: fgName,
		SizeName:  theme.SizeNameText,
	}})
	text.Wrapping = fyne.TextWrapWord
	row := container.NewHBox(
		widget.NewIcon(theme.NewColoredResource(icon, fgName)),
		hSpace(10),
		text,
	)
	return u.cardShell(theme.Color(bgName), theme.Color(bgName), 0, row)
}

func (u *UI) badge(label string, bgName, fgName fyne.ThemeColorName) fyne.CanvasObject {
	background := canvas.NewRectangle(theme.Color(bgName))
	background.CornerRadius = 8
	text := textLine(strings.ToUpper(label), 10, true, theme.Color(fgName))
	inside := container.NewBorder(vSpace(2), vSpace(2), hSpace(8), hSpace(8), text)
	return container.NewStack(background, inside)
}

func (u *UI) statusBadge(status core.SessaoStatus) fyne.CanvasObject {
	switch status {
	case core.SessaoConcluida:
		return u.badge("concluída", colorSoftSuccess, theme.ColorNameSuccess)
	case core.SessaoEmAndamento:
		return u.badge("em andamento", colorSoftPrimary, theme.ColorNamePrimary)
	case core.SessaoFalhou:
		return u.badge("falhou", colorSoftError, theme.ColorNameError)
	case core.SessaoAbortada:
		return u.badge("abortada", colorSoftWarning, theme.ColorNameWarning)
	default:
		return u.badge(string(status), colorSoftPrimary, theme.ColorNamePrimary)
	}
}

func (u *UI) heroMessage(title, message string, tone uiTone) fyne.CanvasObject {
	bgName, fgName, icon := colorSoftSecondary, colorSecondary, theme.InfoIcon()
	if tone == toneSuccess {
		bgName, fgName, icon = colorSoftSuccess, theme.ColorNameSuccess, theme.ConfirmIcon()
	} else if tone == toneWarning {
		bgName, fgName, icon = colorSoftWarning, theme.ColorNameWarning, theme.WarningIcon()
	} else if tone == toneError {
		bgName, fgName, icon = colorSoftError, theme.ColorNameError, theme.ErrorIcon()
	}

	iconBackground := canvas.NewRectangle(theme.Color(colorCardBackground))
	iconBackground.CornerRadius = 14
	iconBackground.SetMinSize(fyne.NewSize(48, 48))
	iconBox := container.NewStack(iconBackground, container.NewCenter(
		widget.NewIcon(theme.NewColoredResource(icon, fgName)),
	))
	text := container.NewVBox(
		textLine(title, 26, true, theme.Color(theme.ColorNameForeground)),
		vSpace(5),
		u.muted(message, theme.SizeNameText),
	)
	return u.cardShell(theme.Color(bgName), theme.Color(bgName), 0, container.NewHBox(
		iconBox, hSpace(15), text,
	))
}

func (u *UI) statTile(label, value, detail string, icon fyne.Resource) fyne.CanvasObject {
	iconBackground := canvas.NewRectangle(theme.Color(colorSoftSecondary))
	iconBackground.CornerRadius = 5
	iconBackground.SetMinSize(fyne.NewSize(32, 32))
	iconBox := container.NewStack(iconBackground, container.NewCenter(
		widget.NewIcon(theme.NewColoredResource(icon, colorSecondary)),
	))

	labelWidget := widget.NewLabel(label)
	labelWidget.SizeName = theme.SizeNameCaptionText
	labelWidget.Wrapping = fyne.TextWrapOff
	texts := container.NewVBox(
		labelWidget,
		textLine(value, 18, true, theme.Color(theme.ColorNameForeground)),
	)
	if detail != "" {
		detailWidget := widget.NewLabel(detail)
		detailWidget.SizeName = theme.SizeNameCaptionText
		detailWidget.Wrapping = fyne.TextWrapWord
		texts.Add(detailWidget)
	}
	left := container.NewHBox(iconBox, hSpace(10))
	return u.cardShell(theme.Color(colorCardBackground), theme.Color(colorBorder), 1, container.NewBorder(
		nil, nil, left, nil, texts,
	))
}

func (u *UI) stepItem(number int, label string) fyne.CanvasObject {
	circle := canvas.NewRectangle(theme.Color(colorSoftSecondary))
	circle.CornerRadius = 4
	circle.SetMinSize(fyne.NewSize(20, 20))
	numberText := textLine(fmt.Sprintf("%d", number), 10, true, theme.Color(colorSecondary))
	return container.NewHBox(
		container.NewStack(circle, container.NewCenter(numberText)),
		hSpace(10),
		widget.NewLabel(label),
	)
}

func (u *UI) sessionCard(sessao core.SessaoResumo, back func()) fyne.CanvasObject {
	id := sessao.ID
	title := fmt.Sprintf("#%d  %s %s", id, displayDistro(sessao.TargetOS), sessao.TargetVersion)
	titleLabel := textLine(title, 15, true, theme.Color(theme.ColorNameForeground))
	status := u.statusBadge(sessao.Status)

	host := sessao.HostDestino
	if host == "" {
		host = "download local"
	}
	subtitle := fmt.Sprintf("%s  •  %s  •  %d pacotes", host, sessao.IniciadaEm.Local().Format("02 Jan 2006 15:04"), sessao.TotalPacotes)
	info := container.NewBorder(nil, nil, nil, status, container.NewVBox(
		titleLabel,
		vSpace(4),
		u.muted(subtitle, theme.SizeNameCaptionText),
	))

	report := u.ghostButton("Relatório", theme.DocumentIcon(), func() {
		u.reportBack = back
		u.showReport(id)
	})
	resume := u.secondaryButton("Retomar", theme.MediaPlayIcon(), func() {
		u.resumeSession(id)
	})
	if sessao.Status == core.SessaoConcluida {
		resume.Disable()
	}
	archive := u.ghostButton("Arquivar", theme.DeleteIcon(), func() {
		if u.store != nil {
			_ = u.store.ArquivarSessao(context.Background(), id, true)
		}
		if back != nil {
			back()
		}
	})
	actions := container.NewHBox(report, resume, archive)

	return u.cardShell(theme.Color(colorCardBackground), theme.Color(colorBorder), 1, container.NewBorder(
		nil, nil, nil, actions, info,
	))
}

func (u *UI) detailRow(label, value string) fyne.CanvasObject {
	valueLabel := widget.NewLabel(value)
	valueLabel.Wrapping = fyne.TextWrapWord
	valueLabel.Selectable = true
	return container.NewVBox(
		u.muted(strings.ToUpper(label), theme.SizeNameCaptionText),
		vSpace(3),
		valueLabel,
	)
}

func displayDistro(value string) string {
	if value == "" {
		return "Linux"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func textLine(text string, size float32, bold bool, color color.Color) *canvas.Text {
	line := canvas.NewText(text, color)
	line.TextSize = size
	line.TextStyle = fyne.TextStyle{Bold: bold}
	return line
}

func vSpace(height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return spacer
}

func hSpace(width float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return spacer
}
