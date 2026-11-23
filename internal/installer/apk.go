package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opf/internal/core"
)

// ApkInstaller installs verified .apk artifacts on the destination.
type ApkInstaller struct {
	remoteDir    string
	useSudo      bool
	sudoPassword string
	reporter     core.PipelineReporter
}

var (
	_ core.InstallExecutor = (*ApkInstaller)(nil)
	_ core.RemoteDirBinder = (*ApkInstaller)(nil)
)

func NewApkInstaller(opts ...Option) *ApkInstaller {
	options := installerOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return &ApkInstaller{useSudo: options.useSudo, sudoPassword: options.sudoPassword, reporter: options.reporter}
}

func (a *ApkInstaller) SetRemoteDir(dir string) error {
	if err := validateRemoteDir(dir); err != nil {
		return err
	}
	a.remoteDir = dir
	return nil
}

func (a *ApkInstaller) InstallCommand(pkgs []core.PackageArtifact) (string, error) {
	if a.remoteDir == "" {
		return "", errors.New("diretório remoto da instalação não configurado")
	}
	names, err := validateAndQuoteArtifacts(pkgs, ".apk")
	if err != nil {
		return "", err
	}

	script := fmt.Sprintf(
		"cd %s && apk add --no-network --allow-untrusted %s",
		shellQuote(a.remoteDir),
		strings.Join(names, " "),
	)
	return wrapSudoCommand(script, a.useSudo, a.sudoPassword)
}

func (a *ApkInstaller) Execute(ctx context.Context, tm core.TransferManager, cmd string) error {
	if tm == nil {
		return errors.New("transferência SSH não configurada para instalação")
	}
	if strings.TrimSpace(cmd) == "" {
		return errors.New("comando de instalação vazio")
	}
	if err := executeInstallCommand(ctx, tm, cmd, a.useSudo, a.sudoPassword, a.reporter); err != nil {
		return fmt.Errorf("executar instalação APK: %w", err)
	}
	return nil
}
