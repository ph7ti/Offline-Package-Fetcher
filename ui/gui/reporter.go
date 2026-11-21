//go:build cgo

package gui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"

	"opf/internal/core"
)

type guiReporter struct {
	ui *UI
}

var _ core.PipelineReporter = (*guiReporter)(nil)

func (r *guiReporter) StepStarted(step int, name string) {
	fyne.Do(func() {
		r.ui.markStep(step, "[▶]")
		r.ui.progressText.SetText(fmt.Sprintf("Passo %d/%d: %s", step, core.TotalEtapas, name))
	})
}

func (r *guiReporter) StepFinished(step int, name string) {
	fyne.Do(func() {
		r.ui.markStep(step, "[✔]")
		r.ui.progressText.SetText(fmt.Sprintf("Passo %d/%d concluído: %s", step, core.TotalEtapas, name))
	})
}

func (r *guiReporter) StepFailed(step int, name string, err error) {
	fyne.Do(func() {
		r.ui.markStep(step, "[✘]")
		r.ui.progressText.SetText(fmt.Sprintf("Falha no passo %d/%d: %s", step, core.TotalEtapas, name))
		if err != nil {
			r.ui.appendLog("[erro] " + err.Error())
		}
	})
}

func (r *guiReporter) Progress(stage string, current, total int64) {
	fyne.Do(func() {
		if total > 0 {
			value := float64(current) / float64(total)
			if value < 0 {
				value = 0
			}
			if value > 1 {
				value = 1
			}
			r.ui.progressBar.SetValue(value)
			r.ui.progressText.SetText(fmt.Sprintf("%s: %d/%d", stage, current, total))
		} else {
			r.ui.progressText.SetText(stage)
		}
	})
}

func (r *guiReporter) Log(level, message string) {
	message = strings.TrimRight(message, "\r\n")
	if strings.TrimSpace(message) == "" {
		return
	}
	if level == "" {
		level = "info"
	}
	fyne.Do(func() {
		r.ui.appendLog("[" + level + "] " + message)
	})
}
