package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opf/internal/core"
)

func TestApkInstallerInstallCommand(t *testing.T) {
	apkInstaller := NewApkInstaller()
	if err := apkInstaller.SetRemoteDir("/tmp/opf-apk"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := apkInstaller.InstallCommand([]core.PackageArtifact{
		{Name: "vim", LocalPath: "/tmp/a/vim-9.1.0707-r0.apk"},
		{Name: "vim-common", LocalPath: "/tmp/a/vim-common-9.1.0707-r0.apk"},
	})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{
		"cd '/tmp/opf-apk'",
		"apk add --no-network --allow-untrusted 'vim-9.1.0707-r0.apk' 'vim-common-9.1.0707-r0.apk'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("comando sem %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--force") {
		t.Errorf("comando contém --force inesperado: %s", cmd)
	}
}

func TestApkInstallerSudoPassword(t *testing.T) {
	apkInstaller := NewApkInstaller(WithSudoPassword("senha"))
	if err := apkInstaller.SetRemoteDir("/tmp/opf-apk"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := apkInstaller.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/vim.apk"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	if !strings.Contains(cmd, "sudo -S -p '' sh -c") {
		t.Fatalf("sudo -S não usado: %s", cmd)
	}
	if strings.Contains(cmd, "senha") {
		t.Fatal("senha não pode aparecer no comando")
	}
	tm := &fakeTransfer{}
	if err := apkInstaller.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(tm.input) != "senha\n" {
		t.Fatalf("stdin = %q", tm.input)
	}
}

func TestApkInstallerValidation(t *testing.T) {
	tests := []struct {
		name      string
		remoteDir string
		artifacts []core.PackageArtifact
		wantErr   string
	}{
		{"sem remote dir", "", []core.PackageArtifact{{LocalPath: "/tmp/a.apk"}}, "diretório remoto"},
		{"sem pacotes", "/tmp/opf", nil, "nenhum pacote"},
		{"não apk", "/tmp/opf", []core.PackageArtifact{{LocalPath: "/tmp/a.deb"}}, "não é .apk"},
		{"duplicado", "/tmp/opf", []core.PackageArtifact{
			{LocalPath: "/tmp/a.apk"}, {LocalPath: "/outro/a.apk"},
		}, "duplicado"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apkInstaller := NewApkInstaller()
			if tt.remoteDir != "" {
				if err := apkInstaller.SetRemoteDir(tt.remoteDir); err != nil {
					t.Fatalf("SetRemoteDir: %v", err)
				}
			}
			_, err := apkInstaller.InstallCommand(tt.artifacts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("esperava erro contendo %q, obteve %v", tt.wantErr, err)
			}
		})
	}
}

func TestApkInstallerExecute(t *testing.T) {
	apkInstaller := NewApkInstaller()
	if err := apkInstaller.SetRemoteDir("/tmp/opf"); err != nil {
		t.Fatalf("SetRemoteDir: %v", err)
	}
	cmd, err := apkInstaller.InstallCommand([]core.PackageArtifact{{LocalPath: "/tmp/a.apk"}})
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	tm := &fakeTransfer{output: "ok"}
	if err := apkInstaller.Execute(context.Background(), tm, cmd); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tm.runError = errors.New("falha remota")
	if err := apkInstaller.Execute(context.Background(), tm, cmd); err == nil || !strings.Contains(err.Error(), "falha remota") {
		t.Fatalf("esperava erro remoto: %v", err)
	}
	if err := apkInstaller.Execute(context.Background(), nil, cmd); err == nil {
		t.Fatal("esperava erro sem transferência")
	}
	if err := apkInstaller.Execute(context.Background(), tm, " "); err == nil {
		t.Fatal("esperava erro para comando vazio")
	}
}
