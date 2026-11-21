package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	opfapp "opf/internal/app"
	"opf/internal/core"
	"opf/internal/fetcher"
	"opf/internal/i18n"
	"opf/internal/inspector"
	"opf/internal/installer"
	"opf/internal/observability"
	"opf/internal/resolver"
	"opf/internal/store"
	"opf/internal/transfer"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "opf-cli:", err)
		os.Exit(1)
	}
}

// applyLanguageFlag selects the interface language before the flags are
// registered so that usage text and flag descriptions are already translated.
// An explicit -lang/--lang value wins over OPF_LANG; auto (or no flag at all)
// follows OPF_LANG and then the operating system language.
func applyLanguageFlag(args []string) error {
	value, found, hasValue := scanLanguageFlag(args)
	if !found {
		i18n.SetFromSetting(i18n.SettingAuto)
		return nil
	}
	if !hasValue {
		i18n.SetFromSetting(i18n.SettingAuto)
		return errors.New(i18n.T("--lang exige um valor"))
	}
	normalized, ok := normalizeLanguageSetting(value)
	if !ok {
		i18n.SetFromSetting(i18n.SettingAuto)
		return errors.New(i18n.T("--lang inválido: %q (use auto, en, pt-BR ou es)", value))
	}
	i18n.SetFromSetting(normalized)
	return nil
}

// scanLanguageFlag looks for -lang/--lang in args before flag parsing. Both
// "-lang value" and "-lang=value" (and the double-dash variants) are accepted;
// like the flag package, the last occurrence wins.
func scanLanguageFlag(args []string) (value string, found, hasValue bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, inlineValue, hasInline := strings.Cut(arg, "=")
		if name != "-lang" && name != "--lang" {
			continue
		}
		found, hasValue = true, true
		switch {
		case hasInline:
			value = inlineValue
		case i+1 < len(args):
			value = args[i+1]
			i++
		default:
			value, hasValue = "", false
		}
	}
	return value, found, hasValue
}

// normalizeLanguageSetting mirrors the values accepted by i18n.Resolve.
func normalizeLanguageSetting(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", i18n.SettingAuto:
		return i18n.SettingAuto, true
	case string(i18n.EN):
		return string(i18n.EN), true
	case strings.ToLower(string(i18n.PTBR)), "pt", "pt_br":
		return string(i18n.PTBR), true
	case string(i18n.ES):
		return string(i18n.ES), true
	default:
		return "", false
	}
}

func run(args []string) error {
	if err := applyLanguageFlag(args); err != nil {
		return err
	}

	fs := flag.NewFlagSet("opf-cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), i18n.T("Uso: opf-cli [opções]"))
		fmt.Fprintln(fs.Output(), i18n.T("Opções:"))
		fs.PrintDefaults()
	}

	fs.String("lang", i18n.SettingAuto, i18n.T("idioma da interface: auto, en, pt-BR ou es"))
	target := fs.String("target", "", i18n.T("destino no formato distro:versao (ex.: ubuntu:22.04, centos:7, alpine:3.20)"))
	arch := fs.String("arch", "", i18n.T("arquitetura do destino (ex.: amd64 ou x86_64)"))
	var packages []string
	fs.Func("pkg", i18n.T("pacote a baixar; pode ser repetido ou separado por vírgula"), func(value string) error {
		for _, pkg := range strings.Split(value, ",") {
			if pkg = strings.TrimSpace(pkg); pkg != "" {
				packages = append(packages, pkg)
			}
		}
		return nil
	})
	forceReinstall := fs.Bool("force-reinstall", false, i18n.T("ignora o pré-check e reinstala pacotes já presentes"))
	imageOverride := fs.String("image", "", i18n.T("imagem Docker alternativa (ex.: opf/centos-7:f3)"))
	fetchMethod := fs.String("fetch-method", "docker", i18n.T("método de fetch: docker ou wsl"))
	wslDistro := fs.String("wsl-distro", "", i18n.T("distro WSL registrada (padrão derivado de --target)"))
	tempDir := fs.String("temp-dir", "", i18n.T("diretório base para temporários do host (padrão: SO)"))
	withOptional := fs.Bool("with-optional", false, i18n.T("inclui dependências opcionais (Recommends/Suggests/Enhances ou weak deps)"))
	outDir := fs.String("out", "./opf-output", i18n.T("diretório base de saída"))
	dbPath := fs.String("db", "./opf.db", i18n.T("caminho do arquivo SQLite"))

	sshHost := fs.String("ssh-host", "", i18n.T("host SSH do destino (habilita a transferência SFTP)"))
	sshPort := fs.Int("ssh-port", 22, i18n.T("porta SSH do destino"))
	sshUser := fs.String("ssh-user", "", i18n.T("usuário SSH do destino"))
	sshKey := fs.String("ssh-key", "", i18n.T("caminho da chave privada SSH"))
	sshPassword := fs.String("ssh-password", "", i18n.T("senha SSH, também usada para sudo -S (prefira a variável OPF_SSH_PASSWORD)"))
	sshKnownHosts := fs.String("ssh-known-hosts", "", i18n.T("caminho do known_hosts (padrão: ~/.ssh/known_hosts)"))
	sshSkipHostKey := fs.Bool("ssh-insecure-skip-host-key", false, i18n.T("ignora a verificação de host key SSH (inseguro; equivale a StrictHostKeyChecking=no)"))
	remoteDir := fs.String("remote-dir", "", i18n.T("diretório remoto de destino (padrão: /tmp/opf-sessao-<id>)"))
	installAfter := fs.Bool("install", false, i18n.T("instala no destino após enviar e validar os pacotes"))
	assumeYes := fs.Bool("yes", false, i18n.T("confirma a instalação sem prompt (requer --install)"))
	useSudo := fs.Bool("sudo", false, i18n.T("executa a instalação via sudo com a senha do usuário SSH"))
	syslogEnabled := fs.Bool("syslog", false, i18n.T("envia eventos ao Syslog externo"))
	syslogHost := fs.String("syslog-host", "", i18n.T("host do Syslog externo"))
	syslogPort := fs.Int("syslog-port", 514, i18n.T("porta do Syslog"))
	syslogProto := fs.String("syslog-proto", "udp", i18n.T("protocolo do Syslog: udp ou tcp"))
	syslogTag := fs.String("syslog-tag", "opf", i18n.T("tag RFC 5424"))
	exportJSON := fs.String("export-json", "", i18n.T("exporta o relatório JSON neste caminho"))
	exportMarkdown := fs.String("export-markdown", "", i18n.T("exporta o relatório Markdown neste caminho"))
	var jumpSpecs []string
	fs.Func("ssh-jump", i18n.T("jump host no formato [usuario@]host[:porta]; pode ser repetido, na ordem do salto"), func(value string) error {
		jumpSpecs = append(jumpSpecs, value)
		return nil
	})
	var jumpAuths []string
	fs.Func("ssh-jump-auth", i18n.T("autenticação da ponte: password, key ou key+password; repetir na ordem de --ssh-jump"), func(value string) error {
		jumpAuths = append(jumpAuths, value)
		return nil
	})
	var jumpPasswords []string
	fs.Func("ssh-jump-password", i18n.T("senha da ponte (prefira a variável OPF_SSH_JUMP_PASSWORDS com array JSON)"), func(value string) error {
		jumpPasswords = append(jumpPasswords, value)
		return nil
	})
	var jumpKeys []string
	fs.Func("ssh-jump-key", i18n.T("caminho da chave privada da ponte; repetir na ordem de --ssh-jump"), func(value string) error {
		jumpKeys = append(jumpKeys, value)
		return nil
	})
	var jumpKnownHosts []string
	fs.Func("ssh-jump-known-hosts", i18n.T("known_hosts da ponte; vazio herda --ssh-known-hosts"), func(value string) error {
		jumpKnownHosts = append(jumpKnownHosts, value)
		return nil
	})

	repoConfigPath := fs.String("repo-config", "", i18n.T("arquivo JSON com a lista de repositórios adicionais"))
	repoURL := fs.String("repo-url", "", i18n.T("URL de um repositório adicional (modo simples)"))
	repoName := fs.String("repo-name", "private", i18n.T("nome do repositório adicional no modo simples"))
	repoFamily := fs.String("repo-family", "", i18n.T("família do repositório adicional: apt, rpm ou apk (padrão: família do alvo)"))
	repoKey := fs.String("repo-key", "", i18n.T("arquivo local da chave do repositório adicional"))
	repoKeyURL := fs.String("repo-key-url", "", i18n.T("URL da chave do repositório adicional"))
	repoCA := fs.String("repo-ca", "", i18n.T("arquivo da CA customizada do repositório adicional"))
	repoAPTSuite := fs.String("repo-apt-suite", "", i18n.T("suite/distribuição do repositório APT"))
	repoAPTComponents := fs.String("repo-apt-components", "main", i18n.T("components do repositório APT, separados por vírgula"))
	repoAPTFlat := fs.Bool("repo-apt-flat", false, i18n.T("repositório APT flat (sem suite/components)"))
	repoInsecureTLS := fs.Bool("repo-insecure-tls", false, i18n.T("ignora a verificação TLS do repositório adicional (inseguro)"))
	repoInsecureGPG := fs.Bool("repo-insecure-gpg", false, i18n.T("ignora a assinatura GPG do repositório adicional (inseguro)"))

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(i18n.T("argumentos inesperados: %s", strings.Join(fs.Args(), " ")))
	}
	if len(packages) == 0 {
		return errors.New(i18n.T("--pkg é obrigatório"))
	}
	if *outDir == "" {
		return errors.New(i18n.T("--out não pode ser vazio"))
	}
	if *dbPath == "" {
		return errors.New(i18n.T("--db não pode ser vazio"))
	}
	if *assumeYes && !*installAfter {
		return errors.New(i18n.T("--yes exige --install"))
	}
	if *useSudo && !*installAfter {
		return errors.New(i18n.T("--sudo exige --install"))
	}
	if *installAfter && *sshHost == "" {
		return errors.New(i18n.T("--install exige --ssh-host"))
	}
	if *syslogEnabled && *syslogHost == "" {
		return errors.New(i18n.T("--syslog exige --syslog-host"))
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	var transferManager core.TransferManager
	var sshConfig core.SSHConfig
	var sshPasswordValue string
	if *sshHost == "" {
		for _, name := range []string{
			"ssh-port", "ssh-user", "ssh-key", "ssh-password",
			"ssh-known-hosts", "remote-dir", "ssh-jump", "ssh-insecure-skip-host-key",
			"ssh-jump-auth", "ssh-jump-password", "ssh-jump-key", "ssh-jump-known-hosts",
		} {
			if setFlags[name] {
				return errors.New(i18n.T("--%s exige --ssh-host", name))
			}
		}
	} else {
		if *sshUser == "" {
			return errors.New(i18n.T("--ssh-user é obrigatório com --ssh-host"))
		}
		if *sshPort <= 0 || *sshPort > 65535 {
			return errors.New(i18n.T("--ssh-port inválida: %d", *sshPort))
		}
		password := *sshPassword
		if password == "" {
			password = os.Getenv("OPF_SSH_PASSWORD")
		}
		if *sshKey == "" && password == "" {
			return errors.New(i18n.T("informe --ssh-key ou --ssh-password/OPF_SSH_PASSWORD"))
		}
		if password != "" {
			logger.Warn(i18n.T("autenticação SSH por senha habilitada; prefira chave privada"))
		}
		sshPasswordValue = password
		sshConfig = core.SSHConfig{
			Host: *sshHost, Port: *sshPort, User: *sshUser, KeyPath: *sshKey,
			Password: password, KnownHosts: *sshKnownHosts, InsecureSkipHostKey: *sshSkipHostKey,
		}
		envPasswords, envErr := decodeJumpPasswordsEnv(os.Getenv("OPF_SSH_JUMP_PASSWORDS"))
		if envErr != nil {
			return envErr
		}
		jumpPasswordValues, mergeErr := mergeJumpPasswords(jumpPasswords, envPasswords)
		if mergeErr != nil {
			return mergeErr
		}
		if err := validateJumpFlagCounts(jumpSpecs, jumpAuths, jumpPasswords, jumpKeys, jumpKnownHosts); err != nil {
			return err
		}
		if len(envPasswords) > 0 && len(envPasswords) != len(jumpSpecs) {
			return errors.New(i18n.T("OPF_SSH_JUMP_PASSWORDS tem %d valores para %d pontes", len(envPasswords), len(jumpSpecs)))
		}
		jumps, jumpErr := buildJumpConfigs(sshConfig, jumpSpecs, jumpAuths, jumpPasswordValues, jumpKeys, jumpKnownHosts)
		if jumpErr != nil {
			return jumpErr
		}
		sshConfig.JumpHosts = jumps
		if len(jumps) > 0 {
			logger.Warn(i18n.T("pontes SSH configuradas; credenciais ficam apenas em memória"))
		}
		transferManager = transfer.NewSSHTransferManager()
	}

	targetWasEmpty := strings.TrimSpace(*target) == ""
	archWasEmpty := strings.TrimSpace(*arch) == ""
	if targetWasEmpty || archWasEmpty {
		if *sshHost == "" {
			return errors.New(i18n.T("--target e --arch são obrigatórios sem --ssh-host"))
		}
		detectCtx, detectCancel := context.WithTimeout(context.Background(), 60*time.Second)
		info, detectErr := opfapp.DetectRemoteTarget(detectCtx, sshConfig)
		detectCancel()
		if detectErr != nil {
			return fmt.Errorf("%s: %w", i18n.T("detectar target remoto"), detectErr)
		}
		if targetWasEmpty {
			*target = info.OS + ":" + info.Version
			if strings.TrimSpace(*imageOverride) == "" && !strings.EqualFold(*fetchMethod, "wsl") {
				*imageOverride = info.Image
			}
		}
		if archWasEmpty {
			*arch = info.Arch
		}
		logger.Info(i18n.T("target detectado por SSH"), "os", info.OS, "version", info.Version, "arch", info.Arch, "family", info.Family, "tested", info.Tested, "image", info.Image)
		if !info.Tested {
			logger.Warn(i18n.T("preset do target detectado ainda não foi validado em integração; use configuração avançada se necessário"))
		}
	}

	targetOS, targetVersion, ok := strings.Cut(*target, ":")
	if !ok || targetOS == "" || targetVersion == "" {
		return errors.New(i18n.T("--target inválido: %q (esperado distro:versao)", *target))
	}
	targetOS = strings.ToLower(targetOS)
	if targetOS == "rocky" {
		targetOS = "rockylinux"
	}
	family := "apt"
	switch targetOS {
	case "ubuntu", "debian":
	case "centos", "rhel", "rockylinux", "almalinux", "fedora":
		family = "rpm"
	case "alpine":
		family = "apk"
	default:
		return errors.New(i18n.T("--target não suportado: %q", targetOS))
	}
	method := strings.ToLower(strings.TrimSpace(*fetchMethod))
	if method != "docker" && method != "wsl" {
		return errors.New(i18n.T("--fetch-method inválido: %q (use docker ou wsl)", *fetchMethod))
	}
	if method == "wsl" {
		if family != "apt" {
			return errors.New(i18n.T("--fetch-method wsl suporta apenas Ubuntu/Debian (APT)"))
		}
		if strings.TrimSpace(*imageOverride) != "" {
			return errors.New(i18n.T("--image não se aplica a --fetch-method wsl"))
		}
	} else if strings.TrimSpace(*wslDistro) != "" {
		return errors.New(i18n.T("--wsl-distro exige --fetch-method wsl"))
	}

	repositories, err := resolveRepositories(repositoryFlags{
		configPath:    *repoConfigPath,
		url:           *repoURL,
		name:          *repoName,
		family:        *repoFamily,
		key:           *repoKey,
		keyURL:        *repoKeyURL,
		ca:            *repoCA,
		aptSuite:      *repoAPTSuite,
		aptComponents: *repoAPTComponents,
		aptFlat:       *repoAPTFlat,
		insecureTLS:   *repoInsecureTLS,
		insecureGPG:   *repoInsecureGPG,
	}, family, setFlags)
	if err != nil {
		return err
	}

	var packageInspector core.PackageInspector
	if transferManager != nil {
		var inspectErr error
		packageInspector, inspectErr = inspector.New(family, transferManager)
		if inspectErr != nil {
			return inspectErr
		}
	}

	var installExecutor core.InstallExecutor
	var confirmInstall core.InstallConfirmation
	var installOptions []installer.Option
	if *useSudo {
		if sshPasswordValue == "" {
			return errors.New(i18n.T("--sudo exige --ssh-password/OPF_SSH_PASSWORD para autenticar o sudo com a senha do usuário SSH"))
		}
		installOptions = append(installOptions, installer.WithSudoPassword(sshPasswordValue))
	}
	if *installAfter {
		switch family {
		case "rpm":
			installExecutor = installer.NewYumDnfInstaller(installOptions...)
		case "apk":
			installExecutor = installer.NewApkInstaller(installOptions...)
		default:
			installExecutor = installer.NewAptInstaller(installOptions...)
		}
		confirmInstall = func(_ context.Context, artifacts []core.PackageArtifact) (bool, error) {
			return confirmInstallation(os.Stdin, os.Stdout, len(artifacts), *sshUser+"@"+*sshHost, *assumeYes)
		}
	}

	repo, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	syslogSink, err := observability.NewSyslogSink(observability.SyslogConfig{
		Enabled: *syslogEnabled, Host: *syslogHost, Port: *syslogPort,
		Proto: *syslogProto, Tag: *syslogTag, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("configurar syslog"), err)
	}
	if syslogSink != nil {
		defer func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = syslogSink.Close(closeCtx)
			closeCancel()
		}()
	}

	fetcherOptions := []fetcher.Option{
		fetcher.WithLogger(logger),
		fetcher.WithResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
			switch family {
			case "rpm":
				return resolver.NewYumDnfResolver(exec, arch, workDir)
			case "apk":
				return resolver.NewApkResolver(exec, arch, workDir)
			default:
				return resolver.NewAptResolver(exec, arch, workDir)
			}
		}),
	}
	if strings.TrimSpace(*imageOverride) != "" {
		fetcherOptions = append(fetcherOptions, fetcher.WithImage(strings.TrimSpace(*imageOverride)))
	}
	if len(repositories) > 0 {
		fetcherOptions = append(fetcherOptions, fetcher.WithRepositories(repositories))
	}
	var packageFetcher core.PackageFetcher
	if method == "wsl" {
		wslOptions := []fetcher.WSLFetcherOption{
			fetcher.WithWSLDistro(*wslDistro),
			fetcher.WithWSLResolverFactory(func(exec core.CommandExecutor, arch, workDir string) core.DependencyResolver {
				return resolver.NewAptResolver(exec, arch, workDir)
			}),
			fetcher.WithWSLTempDir(*tempDir),
			fetcher.WithWSLLogger(logger),
		}
		if len(repositories) > 0 {
			wslOptions = append(wslOptions, fetcher.WithWSLRepositories(repositories))
		}
		packageFetcher = fetcher.NewWSLFetcher(wslOptions...)
	} else {
		packageFetcher = fetcher.NewDockerFetcher(fetcherOptions...)
	}

	sessao := &core.Sessao{
		TargetOS:        targetOS,
		TargetVersion:   targetVersion,
		TargetArch:      strings.ToLower(*arch),
		FetchMethod:     method,
		IncludeOptional: *withOptional,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := core.ExecutarPipeline(ctx, core.PipelineConfig{
		Repo:           repo,
		Fetcher:        packageFetcher,
		Packages:       packages,
		ForceReinstall: *forceReinstall,
		OutputBaseDir:  *outDir,
		Logger:         logger,
		Transfer:       transferManager,
		SSH:            sshConfig,
		RemoteDir:      *remoteDir,
		Installer:      installExecutor,
		ConfirmInstall: confirmInstall,
		Inspector:      packageInspector,
		EventSink:      syslogSink,
		Image:          *imageOverride,
		WSLDistro:      *wslDistro,
	}, sessao)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", i18n.T("sessão %d concluída", result.SessaoID))
	fmt.Printf("%s\n", i18n.T("artefatos: %d", len(result.Artifacts)))
	fmt.Printf("%s\n", i18n.T("saída: %s", result.OutputDir))
	fmt.Printf("%s\n", i18n.T("manifesto: %s", result.CaminhoManifesto))
	if result.RemoteDir != "" {
		fmt.Printf("%s\n", i18n.T("destino remoto: %s", result.RemoteDir))
	}
	if *exportJSON != "" || *exportMarkdown != "" {
		relatorio, reportErr := observability.GerarRelatorio(ctx, repo, result.SessaoID)
		if reportErr != nil {
			return fmt.Errorf("%s: %w", i18n.T("gerar relatório"), reportErr)
		}
		if *exportJSON != "" {
			if err := observability.SalvarJSON(*exportJSON, relatorio); err != nil {
				return err
			}
			fmt.Printf("%s\n", i18n.T("relatório JSON: %s", *exportJSON))
		}
		if *exportMarkdown != "" {
			if err := observability.SalvarMarkdown(*exportMarkdown, relatorio); err != nil {
				return err
			}
			fmt.Printf("%s\n", i18n.T("relatório Markdown: %s", *exportMarkdown))
		}
	}
	if result.Installed {
		fmt.Println(i18n.T("instalação: concluída"))
	}
	if result.InstallDeclined {
		fmt.Println(i18n.T("instalação: recusada; pacotes permanecem enviados"))
	}
	return nil
}

func confirmInstallation(r io.Reader, w io.Writer, count int, target string, assumeYes bool) (bool, error) {
	if assumeYes {
		return true, nil
	}
	fmt.Fprint(w, i18n.T("Instalar %d pacotes em %s? [y/N] ", count, target))
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

const repositoryConfigLimit = 1 << 20

type repositoryFlags struct {
	configPath    string
	url           string
	name          string
	family        string
	key           string
	keyURL        string
	ca            string
	aptSuite      string
	aptComponents string
	aptFlat       bool
	insecureTLS   bool
	insecureGPG   bool
}

func (f repositoryFlags) simpleSet(setFlags map[string]bool) bool {
	for _, name := range []string{
		"repo-url", "repo-name", "repo-family", "repo-key", "repo-key-url",
		"repo-ca", "repo-apt-suite", "repo-apt-components", "repo-apt-flat",
		"repo-insecure-tls", "repo-insecure-gpg",
	} {
		if setFlags[name] {
			return true
		}
	}
	return false
}

func resolveRepositories(flags repositoryFlags, targetFamily string, setFlags map[string]bool) ([]core.PackageRepository, error) {
	simple := flags.simpleSet(setFlags)
	if flags.configPath != "" && simple {
		return nil, errors.New(i18n.T("--repo-config é mutuamente exclusivo com as flags --repo-*"))
	}
	if flags.configPath != "" {
		repos, err := loadRepositoryConfigFile(flags.configPath)
		if err != nil {
			return nil, err
		}
		return core.NormalizePackageRepositories(repos, targetFamily)
	}
	if !simple {
		return nil, nil
	}
	if strings.TrimSpace(flags.url) == "" {
		return nil, errors.New(i18n.T("--repo-url é obrigatório com as flags --repo-*"))
	}

	effectiveFamily := strings.ToLower(strings.TrimSpace(flags.family))
	if effectiveFamily == "" {
		effectiveFamily = targetFamily
	}
	if effectiveFamily != core.RepositoryFamilyAPT {
		for _, name := range []string{"repo-apt-suite", "repo-apt-components", "repo-apt-flat"} {
			if setFlags[name] {
				return nil, errors.New(i18n.T("--%s aplica-se apenas a repositórios APT", name))
			}
		}
	}

	name := strings.TrimSpace(flags.name)
	if name == "" {
		name = "private"
	}
	repo := core.PackageRepository{
		Name:                  name,
		Family:                flags.family,
		URL:                   flags.url,
		KeyFile:               flags.key,
		KeyURL:                flags.keyURL,
		CAFile:                flags.ca,
		InsecureSkipTLSVerify: flags.insecureTLS,
		InsecureSkipGPGCheck:  flags.insecureGPG,
	}
	if effectiveFamily == core.RepositoryFamilyAPT {
		if flags.aptFlat && setFlags["repo-apt-components"] {
			return nil, errors.New(i18n.T("--repo-apt-components não se aplica a --repo-apt-flat"))
		}
		repo.Suite = flags.aptSuite
		repo.Flat = flags.aptFlat
		if !flags.aptFlat {
			repo.Components = splitCommaValues(flags.aptComponents)
		}
	}
	return core.NormalizePackageRepositories([]core.PackageRepository{repo}, targetFamily)
}

func loadRepositoryConfigFile(path string) ([]core.PackageRepository, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("ler --repo-config"), err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New(i18n.T("--repo-config deve apontar para um arquivo regular"))
	}
	if info.Size() > repositoryConfigLimit {
		return nil, errors.New(i18n.T("--repo-config excede %d bytes", repositoryConfigLimit))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("ler --repo-config"), err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var repos []core.PackageRepository
	if err := decoder.Decode(&repos); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("interpretar --repo-config"), err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New(i18n.T("--repo-config contém conteúdo extra após o array JSON"))
	}
	baseDir := filepath.Dir(path)
	for i := range repos {
		if repos[i].KeyFile != "" && !filepath.IsAbs(repos[i].KeyFile) {
			repos[i].KeyFile = filepath.Join(baseDir, repos[i].KeyFile)
		}
		if repos[i].CAFile != "" && !filepath.IsAbs(repos[i].CAFile) {
			repos[i].CAFile = filepath.Join(baseDir, repos[i].CAFile)
		}
	}
	return repos, nil
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func parseJumpHost(spec string, base core.SSHConfig, auth, password, keyPath, knownHosts string) (core.SSHConfig, error) {
	jumps, err := buildJumpConfigs(base, []string{spec}, []string{auth}, []string{password}, []string{keyPath}, []string{knownHosts})
	if err != nil {
		return core.SSHConfig{}, err
	}
	return jumps[0], nil
}

func buildJumpConfigs(base core.SSHConfig, specs, auths, passwords, keys, knownHosts []string) ([]core.SSHConfig, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	inputs := make([]opfapp.SSHJumpInput, 0, len(specs))
	for i, spec := range specs {
		host, port, user, err := parseJumpSpec(spec, base.User)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", i18n.T("--ssh-jump %d", i+1), err)
		}
		inputs = append(inputs, opfapp.SSHJumpInput{
			Host:       host,
			Port:       port,
			User:       user,
			AuthMode:   jumpValueAt(auths, i),
			Password:   jumpValueAt(passwords, i),
			KeyPath:    jumpValueAt(keys, i),
			KnownHosts: jumpValueAt(knownHosts, i),
		})
	}
	return opfapp.BuildSSHJumpHosts(base, inputs)
}

func parseJumpSpec(spec, defaultUser string) (host, port, user string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", "", errors.New(i18n.T("jump host vazio"))
	}

	user = defaultUser
	if at := strings.LastIndex(spec, "@"); at >= 0 {
		user = spec[:at]
		spec = spec[at+1:]
		if user == "" {
			return "", "", "", errors.New(i18n.T("jump host sem usuário antes de @"))
		}
	}
	if spec == "" {
		return "", "", "", errors.New(i18n.T("jump host sem endereço"))
	}

	host = spec
	switch {
	case strings.HasPrefix(spec, "["):
		parsedHost, parsedPort, splitErr := net.SplitHostPort(spec)
		if splitErr != nil {
			return "", "", "", fmt.Errorf("%s: %w", i18n.T("endereço IPv6 do jump inválido"), splitErr)
		}
		if _, portErr := parsePort(parsedPort); portErr != nil {
			return "", "", "", portErr
		}
		host, port = parsedHost, parsedPort
	case strings.Count(spec, ":") == 1:
		parsedHost, parsedPort, splitErr := net.SplitHostPort(spec)
		if splitErr != nil {
			return "", "", "", fmt.Errorf("%s: %w", i18n.T("endereço do jump inválido"), splitErr)
		}
		if _, portErr := parsePort(parsedPort); portErr != nil {
			return "", "", "", portErr
		}
		host, port = parsedHost, parsedPort
	case strings.Count(spec, ":") > 1:
		return "", "", "", errors.New(i18n.T("IPv6 no jump exige brackets: %q", spec))
	}
	return host, port, user, nil
}

func validateJumpFlagCounts(specs, auths, passwords, keys, knownHosts []string) error {
	if len(specs) == 0 {
		for _, item := range []struct {
			name   string
			values []string
		}{
			{"--ssh-jump-auth", auths},
			{"--ssh-jump-password", passwords},
			{"--ssh-jump-key", keys},
			{"--ssh-jump-known-hosts", knownHosts},
		} {
			if len(item.values) > 0 {
				return errors.New(i18n.T("%s exige --ssh-jump", item.name))
			}
		}
		return nil
	}
	if len(auths) == 0 {
		return errors.New(i18n.T("--ssh-jump exige --ssh-jump-auth (password, key ou key+password) por ponte"))
	}
	if len(auths) != len(specs) {
		return errors.New(i18n.T("--ssh-jump-auth tem %d valores para %d pontes", len(auths), len(specs)))
	}
	for _, item := range []struct {
		name   string
		values []string
	}{
		{"--ssh-jump-password", passwords},
		{"--ssh-jump-key", keys},
		{"--ssh-jump-known-hosts", knownHosts},
	} {
		if len(item.values) != 0 && len(item.values) != len(specs) {
			return errors.New(i18n.T("%s tem %d valores para %d pontes", item.name, len(item.values), len(specs)))
		}
	}
	return nil
}

func decodeJumpPasswordsEnv(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, errors.New(i18n.T("OPF_SSH_JUMP_PASSWORDS deve ser um array JSON de strings"))
	}
	return values, nil
}

func mergeJumpPasswords(flagValues, envValues []string) ([]string, error) {
	if len(flagValues) > 0 && len(envValues) > 0 {
		return nil, errors.New(i18n.T("use --ssh-jump-password ou OPF_SSH_JUMP_PASSWORDS, não ambos"))
	}
	if len(envValues) > 0 {
		return envValues, nil
	}
	return flagValues, nil
}

func jumpValueAt(values []string, index int) string {
	if index >= 0 && index < len(values) {
		return values[index]
	}
	return ""
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New(i18n.T("porta do jump inválida: %q", value))
	}
	if port <= 0 || port > 65535 {
		return 0, errors.New(i18n.T("porta do jump fora do intervalo: %d", port))
	}
	return port, nil
}
