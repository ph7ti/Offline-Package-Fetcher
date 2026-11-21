package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opf/internal/core"
)

func TestYumDnfInstallerInstallCommand(t *testing.T) {
	installer := NewYumDnfInstaller()
	if err := installer.SetRemoteDir("/var/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{
		{Name: "vim-enhanced", LocalPath: "/tmp/a/vim-enhanced-7.4.629-8.el7_9.x86_64.rpm"},
		{Name: "vim-common", LocalPath: "/tmp/a/vim-common-7.4.629-8.el7_9.x86_64.rpm"},
	})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{
		"cd '/var/tmp/opf'",
		"if command -v dnf >/dev/null 2>&1; then PM=dnf",
		"then PM=yum",
		"$PM localinstall -y 'vim-enhanced-7.4.629-8.el7_9.x86_64.rpm' 'vim-common-7.4.629-8.el7_9.x86_64.rpm'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("comando sem %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--nogpgcheck") || strings.Contains(cmd, "--force") {
		t.Errorf("comando contém bypass de segurança: %s", cmd)
	}
}

func TestYumDnfInstallerInstallCommandWithSudo(t *testing.T) {
	installer := NewYumDnfInstaller(WithSudoPassword("senha"))
	if err := installer.SetRemoteDir("/var/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	if !strings.Contains(cmd, "sudo -S -p '' sh -c") || !strings.Contains(cmd, "$PM localinstall -y") || !strings.Contains(cmd, "a.rpm") {
		t.Fatalf("sudo não aplicado: %s", cmd)
	}
}

func TestYumDnfInstallerValidation(t *testing.T) {
	tests := []struct {
		name      string
		remoteDir string
		artifacts []core.PackageArtifact
		wantErr   string
	}{
		{"sem remote dir", "", []core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}}, "diretório remoto"},
		{"remote relativo", "rel", []core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}}, "absoluto"},
		{"sem pacotes", "/var/tmp/opf", nil, "nenhum pacote"},
		{"não rpm", "/var/tmp/opf", []core.PackageArtifact{{LocalPath: "/tmp/a.deb"}}, "não é .rpm"},
		{"duplicado", "/var/tmp/opf", []core.PackageArtifact{
			{LocalPath: "/tmp/a.rpm"}, {LocalPath: "/outro/a.rpm"},
		}, "duplicado"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installer := NewYumDnfInstaller()
			if tt.remoteDir != "" {
				err := installer.SetRemoteDir(tt.remoteDir)
				if tt.name == "remote relativo" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("esperava erro contendo %q, obteve %v", tt.wantErr, err)
					}
					return
				}
				if err != nil {
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

func TestYumDnfInstallerExecute(t *testing.T) {
	installer := NewYumDnfInstaller()
	if err := installer.SetRemoteDir("/var/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := installer.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}

	tm := &fakeTransfer{output: "ok"}
	if err := installer.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tm.command != cmd {
		t.Fatalf("comando = %q", tm.command)
	}

	tm.runError = errors.New("falha remota")
	if err := installer.Execute(context.Background(), tm, cmd); err == nil || !strings.Contains(err.Error(), "falha remota") {
		t.Fatalf("esperava erro remoto: %v", err)
	}
	if err := installer.Execute(context.Background(), nil, cmd); err == nil {
		t.Fatal("esperava erro sem transferência")
	}
	if err := installer.Execute(context.Background(), tm, " "); err == nil {
		t.Fatal("esperava erro para comando vazio")
	}
}
