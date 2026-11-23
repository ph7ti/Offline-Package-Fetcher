package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"opf/internal/core"
)

func validateAndQuoteArtifacts(pkgs []core.PackageArtifact, extension string) ([]string, error) {
	if len(pkgs) == 0 {
		return nil, errors.New("nenhum pacote para instalar")
	}
	names := make([]string, 0, len(pkgs))
	seen := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		name := filepath.Base(pkg.LocalPath)
		if name == "" || name == "." || name == ".." {
			return nil, fmt.Errorf("nome de artefato inválido: %q", pkg.LocalPath)
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("artefato com caminho no nome: %q", name)
		}
		if !strings.HasSuffix(name, extension) {
			return nil, fmt.Errorf("artefato não é %s: %q", extension, name)
		}
		if strings.ContainsRune(name, 0) || strings.ContainsAny(name, "\r\n") {
			return nil, fmt.Errorf("nome de artefato inválido: %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("artefato duplicado na instalação: %q", name)
		}
		seen[name] = struct{}{}
		names = append(names, shellQuote(name))
	}
	return names, nil
}

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
	if !useSudo {
		if reporter != nil {
			if streamer, ok := tm.(core.CommandStreamRunner); ok {
				writer := &reporterWriter{reporter: reporter}
				err := streamer.RunCommandStream(ctx, cmd, writer, writer)
				writer.Flush()
				return err
			}
		}
		_, err := tm.RunCommand(ctx, cmd)
		return err
	}
	if password == "" {
		return errors.New("sudo exige a senha do usuário SSH")
	}
	input := []byte(password + "\n")
	if reporter != nil {
		if streamer, ok := tm.(core.CommandStreamInputRunner); ok {
			writer := &reporterWriter{reporter: reporter}
			err := streamer.RunCommandStreamWithInput(ctx, cmd, input, writer, writer)
			writer.Flush()
			return err
		}
	}
	runner, ok := tm.(core.CommandInputRunner)
	if !ok {
		return errors.New("transferência não suporta envio de senha para sudo")
	}
	_, err := runner.RunCommandWithInput(ctx, cmd, input)
	return err
}

type reporterWriter struct {
	mu       sync.Mutex
	reporter core.PipelineReporter
	partial  string
}

func (w *reporterWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial += string(p)
	for {
		idx := strings.IndexByte(w.partial, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(w.partial[:idx])
		w.partial = w.partial[idx+1:]
		if line != "" {
			w.reporter.Log("info", line)
		}
	}
	return len(p), nil
}

func (w *reporterWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	line := strings.TrimSpace(w.partial)
	w.partial = ""
	if line != "" {
		w.reporter.Log("info", line)
	}
}

var _ io.Writer = (*reporterWriter)(nil)
