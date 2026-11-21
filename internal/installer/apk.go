package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	if len(pkgs) == 0 {
		return "", errors.New("nenhum pacote para instalar")
	}

	names := make([]string, 0, len(pkgs))
	seen := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		name := filepath.Base(pkg.LocalPath)
		if name == "" || name == "." || name == ".." {
			return "", fmt.Errorf("nome de artefato inválido: %q", pkg.LocalPath)
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
			return "", fmt.Errorf("artefato com caminho no nome: %q", name)
		}
		if !strings.HasSuffix(name, ".apk") {
			return "", fmt.Errorf("artefato não é .apk: %q", name)
		}
		if strings.ContainsRune(name, 0) || strings.ContainsAny(name, "\r\n") {
			return "", fmt.Errorf("nome de artefato inválido: %q", name)
		}
		if _, ok := seen[name]; ok {
			return "", fmt.Errorf("artefato duplicado na instalação: %q", name)
		}
		seen[name] = struct{}{}
		names = append(names, shellQuote(name))
	}

	script := fmt.Sprintf(
		"cd %s && apk add --allow-untrusted %s",
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
