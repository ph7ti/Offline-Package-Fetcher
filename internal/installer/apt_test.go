package installer

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"opf/internal/core"
)

type fakeTransfer struct {
	command    string
	output     string
	runError   error
	input      []byte
	useInput   bool
	useStream  bool
	streamData string
}

func (f *fakeTransfer) Connect(context.Context, core.SSHConfig) error { return nil }
func (f *fakeTransfer) Upload(context.Context, []string, string) error {
	return nil
}
func (f *fakeTransfer) VerifyChecksums(context.Context, string, string) error { return nil }
func (f *fakeTransfer) RunCommand(_ context.Context, cmd string) (string, error) {
	f.command = cmd
	return f.output, f.runError
}

func (f *fakeTransfer) RunCommandWithInput(_ context.Context, cmd string, input []byte) (string, error) {
	f.command = cmd
	f.useInput = true
	f.input = append([]byte(nil), input...)
	return f.output, f.runError
}
func (f *fakeTransfer) Close() error { return nil }

type streamingFakeTransfer struct {
	fakeTransfer
}

func (f *streamingFakeTransfer) RunCommandStream(_ context.Context, cmd string, stdout, _ io.Writer) error {
	f.command = cmd
	f.useStream = true
	if f.streamData != "" && stdout != nil {
		_, _ = io.WriteString(stdout, f.streamData)
	}
	return f.runError
}

func (f *streamingFakeTransfer) RunCommandStreamWithInput(_ context.Context, cmd string, input []byte, stdout, _ io.Writer) error {
	f.command = cmd
	f.useStream = true
	f.useInput = true
	f.input = append([]byte(nil), input...)
	if f.streamData != "" && stdout != nil {
		_, _ = io.WriteString(stdout, f.streamData)
	}
	return f.runError
}

type fakeReporter struct {
	logs []string
}

func (r *fakeReporter) StepStarted(int, string)       {}
func (r *fakeReporter) StepFinished(int, string)      {}
func (r *fakeReporter) StepFailed(int, string, error) {}
func (r *fakeReporter) Progress(string, int64, int64) {}
func (r *fakeReporter) Log(_, message string)         { r.logs = append(r.logs, message) }

func TestAptInstallerInstallCommand(t *testing.T) {
	installer := NewAptInstaller()
	if err := installer.SetRemoteDir("/tmp/opf-sessao-1"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	artifacts := []core.PackageArtifact{
		{Name: "vim", LocalPath: "/tmp/a/vim_9.0_amd64.deb"},
		{Name: "libc6", LocalPath: "/tmp/a/libc6_2.35_amd64.deb"},
	}
	cmd, err := installer.InstallCommand(artifacts)
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{
		"cd '/tmp/opf-sessao-1'",
		"DEBIAN_FRONTEND=noninteractive dpkg -i 'vim_9.0_amd64.deb' 'libc6_2.35_amd64.deb'",
		`if [ "$status" -ne 0 ]`,
		"DEBIAN_FRONTEND=noninteractive apt-get install -f -y --no-download",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("comando sem %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--allow-unauthenticated") || strings.Contains(cmd, "--force-") {
		t.Errorf("comando contém bypass de segurança: %s", cmd)
	}
}

func TestAptInstallerInstallCommandWithSudo(t *testing.T) {
	installer := NewAptInstaller(WithSudoPassword("senha-secreta"))
	if err := installer.SetRemoteDir("/tmp/opf-sessao-1"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/vim.deb"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{
		"sudo -S -p '' sh -c",
		"DEBIAN_FRONTEND=noninteractive dpkg -i",
		"vim.deb",
		"DEBIAN_FRONTEND=noninteractive apt-get install -f -y --no-download",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("comando sem %q: %s", want, cmd)
		}
	}
}

func TestAptInstallerSudoRequiresPassword(t *testing.T) {
	installer := NewAptInstaller(WithSudo(true))
	if err := installer.SetRemoteDir("/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	if _, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/a.deb"}}); err == nil {
		t.Fatal("esperava erro de sudo sem senha")
	}
}

func TestAptInstallerSudoPasswordFromStdin(t *testing.T) {
	installer := NewAptInstaller(WithSudoPassword("senha-secreta"))
	if err := installer.SetRemoteDir("/tmp/opf-sessao-1"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/vim.deb"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	if !strings.Contains(cmd, "sudo -S -p '' sh -c") {
		t.Fatalf("sudo -S não usado: %s", cmd)
	}
	if strings.Contains(cmd, "senha-secreta") {
		t.Fatal("senha não pode aparecer no comando")
	}

	tm := &fakeTransfer{output: "ok"}
	if err := installer.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !tm.useInput {
		t.Fatal("senha não foi enviada por stdin")
	}
	if string(tm.input) != "senha-secreta\n" {
		t.Fatalf("stdin = %q", tm.input)
	}
}

func TestAptInstallerSetRemoteDirValidation(t *testing.T) {
	installer := NewAptInstaller()
	for _, dir := range []string{"", "relativo", "a\nb", "a\x00b"} {
		if err := installer.SetRemoteDir(dir); err == nil {
			t.Errorf("esperava erro para diretório %q", dir)
		}
	}
}

func TestAptInstallerInstallCommandValidation(t *testing.T) {
	tests := []struct {
		name      string
		remoteDir string
		artifacts []core.PackageArtifact
		wantErr   string
	}{
		{
			name:      "sem remote dir",
			remoteDir: "",
			artifacts: []core.PackageArtifact{{LocalPath: "/tmp/a.deb"}},
			wantErr:   "diretório remoto",
		},
		{
			name:      "sem pacotes",
			remoteDir: "/tmp/opf",
			artifacts: nil,
			wantErr:   "nenhum pacote",
		},
		{
			name:      "não deb",
			remoteDir: "/tmp/opf",
			artifacts: []core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}},
			wantErr:   "não é .deb",
		},
		{
			name:      "duplicado",
			remoteDir: "/tmp/opf",
			artifacts: []core.PackageArtifact{
				{LocalPath: "/tmp/a.deb"},
				{LocalPath: "/other/a.deb"},
			},
			wantErr: "duplicado",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installer := NewAptInstaller()
			if tt.remoteDir != "" {
				if err := installer.SetRemoteDir(tt.remoteDir); err != nil {
					t.Fatalf("SetRemoteDir: %v", err)
				}
			}
			_, err := installer.InstallCommand(tt.artifacts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("esperava erro contendo %q, obteve %v", tt.wantErr, err)
			}
		})
	}
}

func TestAptInstallerExecute(t *testing.T) {
	installer := NewAptInstaller()
	if err := installer.SetRemoteDir("/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/a.deb"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}

	tm := &fakeTransfer{output: "ok"}
	if err := installer.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tm.command != cmd {
		t.Fatalf("comando executado = %q", tm.command)
	}

	tm.runError = errors.New("falha remota")
	if err := installer.Execute(context.Background(), tm, cmd); err == nil || !strings.Contains(err.Error(), "falha remota") {
		t.Fatalf("esperava erro remoto: %v", err)
	}

	if err := installer.Execute(context.Background(), nil, cmd); err == nil {
		t.Fatal("esperava erro sem transferência")
	}
	if err := installer.Execute(context.Background(), tm, "  "); err == nil {
		t.Fatal("esperava erro para comando vazio")
	}
}

func TestAptInstallerStreamsOutputWithSudo(t *testing.T) {
	reporter := &fakeReporter{}
	installer := NewAptInstaller(WithSudoPassword("senha-secreta"), WithReporter(reporter))
	if err := installer.SetRemoteDir("/tmp/opf-sessao-1"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/vim.deb"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	tm := &streamingFakeTransfer{fakeTransfer: fakeTransfer{streamData: "Setting up vim...\nDone"}}
	if err := installer.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !tm.useStream || !tm.useInput {
		t.Fatalf("esperava streaming com stdin: stream=%v input=%v", tm.useStream, tm.useInput)
	}
	if string(tm.input) != "senha-secreta\n" {
		t.Fatalf("stdin = %q", tm.input)
	}
	if len(reporter.logs) != 2 || reporter.logs[0] != "Setting up vim..." || reporter.logs[1] != "Done" {
		t.Fatalf("logs recebidos = %v", reporter.logs)
	}
}
