package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"opf/internal/core"
)

// AptInstaller installs the verified .deb artifacts on the destination with
// dpkg and finishes dependency configuration with apt-get.
type installerOptions struct {
	useSudo      bool
	sudoPassword string
	reporter     core.PipelineReporter
}

type Option func(*installerOptions)

// WithSudo enables sudo. A password is mandatory and must be provided with
// WithSudoPassword; it is sent to sudo only through stdin.
func WithSudo(use bool) Option {
	return func(o *installerOptions) { o.useSudo = use }
}

// WithSudoPassword enables sudo and authenticates it with the SSH user's
// password, sent only through stdin.
func WithReporter(reporter core.PipelineReporter) Option {
	return func(o *installerOptions) { o.reporter = reporter }
}

func WithSudoPassword(password string) Option {
	return func(o *installerOptions) {
		o.sudoPassword = password
		if password != "" {
			o.useSudo = true
		}
	}
}

type AptInstaller struct {
	remoteDir    string
	useSudo      bool
	sudoPassword string
	reporter     core.PipelineReporter
}

var (
	_ core.InstallExecutor = (*AptInstaller)(nil)
	_ core.RemoteDirBinder = (*AptInstaller)(nil)
)

func NewAptInstaller(opts ...Option) *AptInstaller {
	options := installerOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return &AptInstaller{useSudo: options.useSudo, sudoPassword: options.sudoPassword, reporter: options.reporter}
}

func (a *AptInstaller) SetRemoteDir(dir string) error {
	if err := validateRemoteDir(dir); err != nil {
		return err
	}
	a.remoteDir = dir
	return nil
}

func (a *AptInstaller) InstallCommand(pkgs []core.PackageArtifact) (string, error) {
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
		if !strings.HasSuffix(name, ".deb") {
			return "", fmt.Errorf("artefato não é .deb: %q", name)
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

	dpkgCommand := "DEBIAN_FRONTEND=noninteractive dpkg -i " + strings.Join(names, " ")
	aptCommand := "DEBIAN_FRONTEND=noninteractive apt-get install -f -y"

	// Caso o dpkg encontre dependências ainda não configuradas, executa
	// apt-get -f para concluir a configuração sem depender de rede.
	script := fmt.Sprintf(
		"cd %s && { %s; status=$?; "+
			"if [ \"$status\" -ne 0 ]; then %s; fi; }",
		shellQuote(a.remoteDir),
		dpkgCommand,
		aptCommand,
	)
	return wrapSudoCommand(script, a.useSudo, a.sudoPassword)
}

func (a *AptInstaller) Execute(ctx context.Context, tm core.TransferManager, cmd string) error {
	if tm == nil {
		return errors.New("transferência SSH não configurada para instalação")
	}
	if strings.TrimSpace(cmd) == "" {
		return errors.New("comando de instalação vazio")
	}
	if err := executeInstallCommand(ctx, tm, cmd, a.useSudo, a.sudoPassword, a.reporter); err != nil {
		return fmt.Errorf("executar instalação APT: %w", err)
	}
	return nil
}

func validateRemoteDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("diretório remoto vazio")
	}
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("diretório remoto deve ser absoluto: %q", dir)
	}
	if strings.ContainsRune(dir, 0) || strings.ContainsAny(dir, "\r\n") {
		return fmt.Errorf("diretório remoto inválido: %q", dir)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
