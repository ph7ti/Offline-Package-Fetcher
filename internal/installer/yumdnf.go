package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"opf/internal/core"
)

// YumDnfInstaller installs RPM artifacts with yum/dnf localinstall.
type YumDnfInstaller struct {
	remoteDir    string
	useSudo      bool
	sudoPassword string
	reporter     core.PipelineReporter
}

var (
	_ core.InstallExecutor = (*YumDnfInstaller)(nil)
	_ core.RemoteDirBinder = (*YumDnfInstaller)(nil)
)

func NewYumDnfInstaller(opts ...Option) *YumDnfInstaller {
	options := installerOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return &YumDnfInstaller{useSudo: options.useSudo, sudoPassword: options.sudoPassword, reporter: options.reporter}
}

func (i *YumDnfInstaller) SetRemoteDir(dir string) error {
	if err := validateRemoteDir(dir); err != nil {
		return err
	}
	i.remoteDir = dir
	return nil
}

func (i *YumDnfInstaller) InstallCommand(pkgs []core.PackageArtifact) (string, error) {
	if i.remoteDir == "" {
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
		if !strings.HasSuffix(name, ".rpm") {
			return "", fmt.Errorf("artefato não é .rpm: %q", name)
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

	installCommand := "$PM localinstall -y " + strings.Join(names, " ")
	script := fmt.Sprintf(
		"cd %s && { if command -v dnf >/dev/null 2>&1; then PM=dnf; "+
			"elif command -v yum >/dev/null 2>&1; then PM=yum; "+
			"else echo 'yum/dnf não encontrado' >&2; exit 1; fi; %s; }",
		shellQuote(i.remoteDir),
		installCommand,
	)
	return wrapSudoCommand(script, i.useSudo, i.sudoPassword)
}

func (i *YumDnfInstaller) Execute(ctx context.Context, tm core.TransferManager, cmd string) error {
	if tm == nil {
		return errors.New("transferência SSH não configurada para instalação")
	}
	if strings.TrimSpace(cmd) == "" {
		return errors.New("comando de instalação vazio")
	}
	if err := executeInstallCommand(ctx, tm, cmd, i.useSudo, i.sudoPassword, i.reporter); err != nil {
		return fmt.Errorf("executar instalação YUM/DNF: %w", err)
	}
	return nil
}
