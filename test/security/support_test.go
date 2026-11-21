// Package security contains the OPF security regression suite. Every test in
// this package asserts observable behaviour of the production code (exported
// APIs, SQLite state, generated shell commands and parsed artefacts). Tests
// that need unexported functions live in narrowly scoped *_test.go files next
// to the code they exercise.
//
// The suite is intentionally dependency-free: it uses dummy secrets, temporary
// directories and fake executors, so it can run in CI without Docker, WSL or
// SSH access.
package security

import (
	"context"
	"strings"
	"testing"

	"opf/internal/core"
	"opf/internal/installer"
)

// installExecutor is the common surface of the three installers.
type installExecutor interface {
	InstallCommand(pkgs []core.PackageArtifact) (command string, err error)
	SetRemoteDir(dir string) error
	Execute(ctx context.Context, tm core.TransferManager, cmd string) error
}

// installersFor builds one installer per family with the same options.
func installersFor(t *testing.T, opts ...installer.Option) map[string]installExecutor {
	t.Helper()
	return map[string]installExecutor{
		"apt": installer.NewAptInstaller(opts...),
		"rpm": installer.NewYumDnfInstaller(opts...),
		"apk": installer.NewApkInstaller(opts...),
	}
}

func artifactSuffix(family string) string {
	switch family {
	case "rpm":
		return ".rpm"
	case "apk":
		return ".apk"
	default:
		return ".deb"
	}
}

// singleQuote mirrors the POSIX single-quote escape used by the production
// helpers: ' -> '\”. The white-box tests verify the production helper itself;
// here it is only used to prove the command embeds that exact token.
func singleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// quoteState returns, for every byte of command, whether that byte is inside a
// POSIX single-quoted word. Backslash escapes outside quotes are honoured, and
// the '\” close/escape/reopen sequence is handled by toggling on quotes.
func quoteState(command string) []bool {
	state := make([]bool, len(command))
	inQuote := false
	for i := 0; i < len(command); i++ {
		state[i] = inQuote
		switch command[i] {
		case '\'':
			inQuote = !inQuote
		case '\\':
			if !inQuote && i+1 < len(command) {
				i++
				state[i] = false
			}
		}
	}
	return state
}

// assertOnlyQuoted fails when payload appears in command outside a
// single-quoted segment. Payloads containing a single quote are split by the
// quoting sequence and therefore never appear contiguously; that is safe and
// the caller asserts the quoted token separately.
func assertOnlyQuoted(t *testing.T, command, payload string) {
	t.Helper()
	state := quoteState(command)
	for offset := 0; offset <= len(command)-len(payload); {
		index := strings.Index(command[offset:], payload)
		if index < 0 {
			return
		}
		index += offset
		if !state[index] {
			t.Fatalf("payload %q appears outside single quotes in command: %s", payload, command)
		}
		offset = index + 1
	}
}

// countingExecutor records every command handed to the isolated environment.
type countingExecutor struct {
	outputs []string
	err     error
	calls   [][]string
}

func (f *countingExecutor) Exec(_ context.Context, cmd []string) (string, error) {
	call := append([]string(nil), cmd...)
	f.calls = append(f.calls, call)
	if f.err != nil {
		return "", f.err
	}
	index := len(f.calls) - 1
	if index < len(f.outputs) {
		return f.outputs[index], nil
	}
	return "", nil
}

// fakeTransferManager is a TransferManager stub that records remote commands
// and the stdin sent by sudo -S.
type fakeTransferManager struct {
	output   string
	err      error
	commands []string
	input    []byte
}

func (f *fakeTransferManager) Connect(context.Context, core.SSHConfig) error { return nil }

func (f *fakeTransferManager) Upload(context.Context, []string, string) error { return nil }

func (f *fakeTransferManager) VerifyChecksums(context.Context, string, string) error { return nil }

func (f *fakeTransferManager) RunCommand(_ context.Context, cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	return f.output, f.err
}

// RunCommandWithInput implements core.CommandInputRunner, the contract used by
// sudo -S so the password never reaches argv.
func (f *fakeTransferManager) RunCommandWithInput(_ context.Context, cmd string, input []byte) (string, error) {
	f.commands = append(f.commands, cmd)
	f.input = append([]byte(nil), input...)
	return f.output, f.err
}

func (f *fakeTransferManager) Close() error { return nil }
