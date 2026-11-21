package installer

import (
	"context"
	"errors"
	"io"
	"strings"

	"opf/internal/core"
)

func wrapSudoCommand(inner string, useSudo bool, password string) (string, error) {
	if !useSudo {
		return inner, nil
	}
	if password == "" {
		return "", errors.New("sudo exige a senha do usuário SSH")
	}
	if strings.ContainsAny(password, "\r\n\x00") {
		return "", errors.New("senha de sudo contém caractere inválido")
	}
	return "sudo -S -p '' sh -c " + shellQuote(inner), nil
}

func executeInstallCommand(ctx context.Context, tm core.TransferManager, cmd string, useSudo bool, password string, reporter core.PipelineReporter) error {
	if (!useSudo || password == "") && reporter != nil {
		if streamer, ok := tm.(core.CommandStreamRunner); ok {
			writer := &reporterWriter{reporter: reporter}
			if err := streamer.RunCommandStream(ctx, cmd, writer, writer); err != nil {
				return err
			}
			return nil
		}
	}
	if !useSudo {
		if _, err := tm.RunCommand(ctx, cmd); err != nil {
			return err
		}
		return nil
	}
	if password == "" {
		return errors.New("sudo exige a senha do usuário SSH")
	}
	runner, ok := tm.(core.CommandInputRunner)
	if !ok {
		return errors.New("transferência não suporta envio de senha para sudo")
	}
	_, err := runner.RunCommandWithInput(ctx, cmd, []byte(password+"\n"))
	return err
}

type reporterWriter struct {
	reporter core.PipelineReporter
}

func (w *reporterWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			w.reporter.Log("info", line)
		}
	}
	return len(p), nil
}

var _ io.Writer = (*reporterWriter)(nil)
