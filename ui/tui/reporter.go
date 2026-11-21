package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"opf/internal/core"
	"opf/internal/i18n"
)

type tuiReporter struct {
	events chan tea.Msg
}

var _ core.PipelineReporter = (*tuiReporter)(nil)

func (r *tuiReporter) StepStarted(step int, name string) {
	r.send(pipelineStepMsg{step: step, status: "[▶]", name: name})
}

func (r *tuiReporter) StepFinished(step int, name string) {
	r.send(pipelineStepMsg{step: step, status: "[✔]", name: name})
}

func (r *tuiReporter) StepFailed(step int, name string, err error) {
	r.send(pipelineStepMsg{step: step, status: "[✘]", name: name})
	if err != nil {
		r.send(pipelineLogMsg{level: i18n.T("erro"), message: err.Error()})
	}
}

func (r *tuiReporter) Progress(stage string, current, total int64) {
	r.send(pipelineProgressMsg{stage: stage, current: current, total: total})
}

func (r *tuiReporter) Log(level, message string) {
	r.send(pipelineLogMsg{level: level, message: message})
}

func (r *tuiReporter) send(msg tea.Msg) {
	select {
	case r.events <- msg:
	default:
		// Progress UI remains responsive; completion messages use the same
		// buffered channel and are sent by the pipeline goroutine directly.
	}
}
