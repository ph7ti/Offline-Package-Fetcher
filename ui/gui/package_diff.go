//go:build cgo

package gui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	opfapp "opf/internal/app"
	"opf/internal/core"
	"opf/internal/i18n"
)

// versionReportOptions returns the radio labels for the version collection
// mode in the active interface language. "Default" is kept untranslated.
func versionReportOptions() []string {
	return []string{i18n.T("Todos"), "Default", i18n.T("Nenhum")}
}

// versionReportLabel maps a collection mode to its radio label.
func versionReportLabel(mode core.VersionReportMode) string {
	switch mode {
	case core.VersionReportAll:
		return i18n.T("Todos")
	case core.VersionReportNone:
		return i18n.T("Nenhum")
	default:
		return "Default"
	}
}

// versionReportModeFromLabel maps a radio label (in any supported language)
// back to the collection mode. Unknown labels fall back to Default.
func versionReportModeFromLabel(selected string) core.VersionReportMode {
	switch selected {
	case i18n.T("Todos"), "Todos", "All":
		return core.VersionReportAll
	case i18n.T("Nenhum"), "Nenhum", "None":
		return core.VersionReportNone
	default:
		return core.VersionReportDefault
	}
}

// selectedVersionReportMode reads the mode currently selected on the packages
// screen. The default is Default, matching a new session.
func (u *UI) selectedVersionReportMode() core.VersionReportMode {
	if u.versionReportGroup == nil {
		return core.VersionReportDefault
	}
	if u.sshEnabled != nil && !u.sshEnabled.Checked {
		return core.VersionReportNone
	}
	return versionReportModeFromLabel(u.versionReportGroup.Selected)
}

// syncVersionReportSelect rebuilds the radio options in the active language
// while preserving the chosen mode. While SSH is disabled the collection is
// forced to None and the widget is disabled; enabling SSH restores Default.
func (u *UI) syncVersionReportSelect() {
	if u.versionReportGroup == nil {
		return
	}
	mode := versionReportModeFromLabel(u.versionReportGroup.Selected)
	if !u.sshEnabled.Checked {
		mode = core.VersionReportNone
	} else if u.versionReportGroup.Disabled() {
		mode = core.VersionReportDefault
	}
	u.versionReportGroup.Options = versionReportOptions()
	u.versionReportGroup.SetSelected(versionReportLabel(mode))
	u.versionReportGroup.Refresh()
	if u.sshEnabled.Checked {
		u.versionReportGroup.Enable()
	} else {
		u.versionReportGroup.Disable()
	}
}

// showPackageDiffLoading paints the intermediate screen shown while installed
// and candidate versions are collected over SSH. No package is downloaded yet.
func (u *UI) showPackageDiffLoading() {
	status := u.muted(i18n.T("Coletando versões instaladas e candidatas..."), theme.SizeNameSubHeadingText)
	loadingCard := u.sectionCard(i18n.T("Package diff"), i18n.T("Comparando as versões instaladas no destino com as candidatas."), container.NewVBox(
		widget.NewProgressBarInfinite(),
		vSpace(10),
		status,
	))
	content := container.NewVBox(
		u.banner(i18n.T("O ambiente remoto está sendo consultado; nenhum pacote foi baixado ainda."), toneInfo),
		vSpace(12),
		loadingCard,
		vSpace(12),
		container.NewHBox(layout.NewSpacer(), u.dangerButton(i18n.T("Cancelar execução"), theme.CancelIcon(), u.cancelRun)),
	)
	u.showScreen("Package diff", content)
}

// showPackageDiff renders the upgrades found before the pipeline starts. It is
// only opened when at least one upgrade exists.
func (u *UI) showPackageDiff() {
	if u.preview == nil {
		u.showPackages()
		return
	}
	diff := u.preview.Data.Diff
	rows := make([]fyne.CanvasObject, 0, len(diff)*2+2)
	rows = append(rows, u.diffHeaderRow(), widget.NewSeparator())
	for _, item := range diff {
		rows = append(rows, vSpace(4), u.diffTableRow(item), vSpace(4), widget.NewSeparator())
	}
	tableCard := u.sectionCard(
		i18n.T("Atualizações detectadas"),
		i18n.T("%d pacotes serão atualizados.", len(diff)),
		container.NewVBox(rows...),
	)

	proceedLabel := i18n.T("Prosseguir com a instalação")
	if !u.installCheck.Checked {
		proceedLabel = i18n.T("Prosseguir com download")
	}
	content := container.NewVBox(
		u.banner(i18n.T("Estes pacotes já estão instalados no destino e serão atualizados para as versões candidatas. Revise antes de prosseguir."), toneInfo),
		vSpace(12),
		tableCard,
		vSpace(10),
		u.muted(i18n.T("Modo de coleta: %s", versionReportLabel(u.preview.Data.Mode)), theme.SizeNameCaptionText),
		vSpace(10),
		container.NewHBox(
			u.dangerButton(i18n.T("Abortar"), theme.CancelIcon(), u.abortPackageDiff),
			layout.NewSpacer(),
			u.primaryButton(proceedLabel, theme.ConfirmIcon(), u.proceedPackageDiff),
		),
	)
	u.showScreen("Package diff", content)
}

func (u *UI) diffHeaderRow() fyne.CanvasObject {
	header := func(text string) fyne.CanvasObject {
		return textLine(text, 11, true, theme.Color(theme.ColorNameForeground))
	}
	return container.NewGridWithColumns(5,
		header(i18n.T("Pacote")),
		header(i18n.T("Versão instalada")),
		header(i18n.T("Versão candidata")),
		header(i18n.T("Salto")),
		header(i18n.T("Papel")),
	)
}

func (u *UI) diffTableRow(item core.PackageDiff) fyne.CanvasObject {
	return container.NewGridWithColumns(5,
		diffCell(item.Name),
		diffCell(item.From),
		diffCell(item.To),
		diffCell(fmt.Sprintf("%s → %s", item.From, item.To)),
		diffCell(diffRoleLabel(item)),
	)
}

func diffCell(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Truncation = fyne.TextTruncateEllipsis
	return label
}

func diffRoleLabel(item core.PackageDiff) string {
	if item.IsDependency {
		return i18n.T("dependência")
	}
	return i18n.T("principal")
}

// showPreviewProgress opens the Pipeline screen with step 1 already complete,
// because the preview collected versions and created the session.
func (u *UI) showPreviewProgress() {
	u.showProgress()
	u.markStep(1, "[✔]")
	u.progressText.SetText(i18n.T("Passo %d/%d concluído: %s", 1, core.TotalEtapas, core.NomeEtapa(1)))
}

// finishPreviewError restores the packages screen and closes the preview
// service, which the caller already closed. Cancellation is reported as an
// informational dialog instead of an alarming error.
func (u *UI) finishPreviewError(err error) {
	u.running = false
	u.cancel = nil
	u.runCtx = nil
	u.preview = nil
	u.previewSvc = nil
	u.openStore()
	u.showPackages()
	if errors.Is(err, context.Canceled) {
		dialog.ShowInformation(i18n.T("Package diff"), i18n.T("Coleta de versões cancelada."), u.window)
		return
	}
	u.showError(err)
}

// proceedPackageDiff runs the normal pipeline from step 2 with the previewed
// upgrade roots and then closes the preview service.
func (u *UI) proceedPackageDiff() {
	preview := u.preview
	svc := u.previewSvc
	if preview == nil || svc == nil {
		return
	}
	u.preview = nil
	u.previewSvc = nil
	ctx := u.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	u.showPreviewProgress()
	go func() {
		result, runErr := svc.RunPreviewed(ctx, preview, u.reporter)
		closeErr := svc.Close()
		if runErr == nil && closeErr != nil {
			runErr = closeErr
		}
		fyne.Do(func() { u.finishRun(result, runErr) })
	}()
}

// abortPackageDiff records the refusal and finalizes the preview session as
// aborted. Nothing is downloaded or installed.
func (u *UI) abortPackageDiff() {
	preview := u.preview
	svc := u.previewSvc
	u.preview = nil
	u.previewSvc = nil
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	u.runCtx = nil
	u.running = false
	u.result = nil
	u.runErr = nil
	if preview != nil && preview.SessionID > 0 {
		u.sessionID = preview.SessionID
		u.resumeID = 0
	}

	abortErr := error(nil)
	if preview != nil && svc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		abortErr = svc.AbortPreview(ctx, preview)
		cancel()
		closeErr := svc.Close()
		if abortErr == nil && closeErr != nil {
			abortErr = closeErr
		}
	}
	u.openStore()
	if abortErr != nil {
		u.showPackages()
		u.showError(abortErr)
		return
	}
	info := dialog.NewInformation(
		i18n.T("Sessão abortada"),
		i18n.T("Sessão abortada na tela Package diff; o relatório mantém as versões e a decisão."),
		u.window,
	)
	info.SetOnClosed(u.showPackages)
	info.Show()
}

// abortPackageDiffForClose finalizes an open preview when the window is closed
// before the user decides, so the session does not stay "in progress".
func (u *UI) abortPackageDiffForClose() {
	if u.preview == nil || u.previewSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = u.previewSvc.AbortPreview(ctx, u.preview)
	_ = u.previewSvc.Close()
	u.preview = nil
	u.previewSvc = nil
}

// runPackagePreview collects installed and candidate versions, then either
// starts the pipeline directly (no upgrades) or opens the Package diff screen.
// The service stays open until the user proceeds or aborts.
func (u *UI) runPackagePreview(ctx context.Context, cfg opfapp.Config) {
	if cfg.Install && cfg.SSH.Host != "" {
		isRoot, probeErr := opfapp.DetectRoot(ctx, cfg.SSH)
		if probeErr != nil {
			fyne.Do(func() {
				u.finishPreviewError(fmt.Errorf("%s: %w", i18n.T("detectar privilégios remotos"), probeErr))
			})
			return
		}
		if isRoot {
			cfg.UseSudo = false
			cfg.SudoPassword = ""
		} else if !cfg.UseSudo {
			fyne.Do(func() {
				u.finishPreviewError(errors.New(i18n.T("usuário remoto não é root; habilite “Usar sudo” e informe a senha da conta")))
			})
			return
		}
	}
	svc, createErr := opfapp.New(cfg)
	if createErr != nil {
		fyne.Do(func() { u.finishPreviewError(createErr) })
		return
	}
	preview, previewErr := svc.PreviewPackageDiff(ctx)
	if previewErr != nil {
		_ = svc.Close()
		fyne.Do(func() { u.finishPreviewError(previewErr) })
		return
	}
	if preview == nil {
		// Defensive fallback: the preview path is only entered for a mode that
		// collects versions, but a nil preview must still run the pipeline.
		fyne.Do(func() { u.showProgress() })
		result, runErr := svc.Run(ctx)
		closeErr := svc.Close()
		if runErr == nil && closeErr != nil {
			runErr = closeErr
		}
		fyne.Do(func() { u.finishRun(result, runErr) })
		return
	}
	if len(preview.Data.Diff) == 0 {
		fyne.Do(func() { u.showPreviewProgress() })
		result, runErr := svc.RunPreviewed(ctx, preview, u.reporter)
		closeErr := svc.Close()
		if runErr == nil && closeErr != nil {
			runErr = closeErr
		}
		fyne.Do(func() { u.finishRun(result, runErr) })
		return
	}
	fyne.Do(func() {
		u.preview = preview
		u.previewSvc = svc
		u.sessionID = preview.SessionID
		u.showPackageDiff()
	})
}
