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
	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/i18n"
	"opf/internal/observability"
	"opf/internal/transfer"
	"opf/internal/version"
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
	showVersion := fs.Bool("version", false, i18n.T("exibe a versão e metadados de build"))
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
	pkgFile := fs.String("pkg-file", "", i18n.T("arquivo texto com lista de pacotes (um por linha ou separados por vírgula; # inicia comentário)"))
	resumeID := fs.Int64("resume", 0, i18n.T("retoma uma sessão existente pelo ID"))
	forceReinstall := fs.Bool("force-reinstall", false, i18n.T("ignora o pré-check e reinstala pacotes já presentes"))
	versionReport := fs.String("version-report", "", i18n.T("coleta de versões instaladas: all, default ou none"))
	imageOverride := fs.String("image", "", i18n.T("imagem Docker alternativa (ex.: opf/centos-7:f3)"))
	fetchMethod := fs.String("fetch-method", "docker", i18n.T("método de fetch: docker, podman ou wsl"))
	containerHost := fs.String("container-host", "", i18n.T("URI do daemon Docker/Podman (ex.: unix:///run/user/1000/podman/podman.sock)"))
	wslDistro := fs.String("wsl-distro", "", i18n.T("distro WSL registrada (padrão derivado de --target)"))
	tempDir := fs.String("temp-dir", "", i18n.T("diretório base para temporários do host (padrão: SO)"))
	withOptional := fs.Bool("with-optional", false, i18n.T("inclui dependências opcionais (Recommends/Suggests/Enhances ou weak deps)"))
	outDir := fs.String("out", "./opf-output", i18n.T("diretório base de saída"))
	offlineBundle := fs.String("offline-bundle", "", i18n.T("gera bundle air-gapped .tar.gz com install-offline.sh (use 'auto' ou um caminho)"))
	dbPath := fs.String("db", "./opf.db", i18n.T("caminho do arquivo SQLite"))

	httpProxy := fs.String("http-proxy", "", i18n.T("proxy HTTP para o contêiner isolado"))
	httpsProxy := fs.String("https-proxy", "", i18n.T("proxy HTTPS para o contêiner isolado"))
	noProxy := fs.String("no-proxy", "", i18n.T("lista NO_PROXY para o contêiner isolado"))
	inheritProxy := fs.Bool("inherit-proxy", false, i18n.T("herda HTTP_PROXY, HTTPS_PROXY e NO_PROXY do ambiente atual"))
	caCert := fs.String("ca-cert", "", i18n.T("certificado CA corporativo PEM instalado no contêiner isolado"))

	profileName := fs.String("profile", "", i18n.T("carrega um perfil salvo de sessão (sem segredos)"))
	saveProfileName := fs.String("save-profile", "", i18n.T("salva a configuração atual como perfil (sem segredos)"))
	listProfiles := fs.Bool("list-profiles", false, i18n.T("lista os perfis salvos e encerra"))
	deleteProfileName := fs.String("delete-profile", "", i18n.T("remove um perfil salvo e encerra"))
	profilesFile := fs.String("profiles-file", "", i18n.T("arquivo alternativo de perfis (padrão: profiles.json do usuário)"))

	sshConfigAlias := fs.String("ssh-config", "", i18n.T("importa host, usuário, porta, chave e ProxyJump de ~/.ssh/config"))
	sshConfigFile := fs.String("ssh-config-file", "", i18n.T("caminho alternativo do arquivo ssh config"))
	sshHost := fs.String("ssh-host", "", i18n.T("host SSH do destino (habilita a transferência SFTP)"))
	sshPort := fs.Int("ssh-port", 22, i18n.T("porta SSH do destino"))
	sshUser := fs.String("ssh-user", "", i18n.T("usuário SSH do destino"))
	sshKey := fs.String("ssh-key", "", i18n.T("caminho da chave privada SSH"))
	sshKeyPassphrase := fs.String("ssh-key-passphrase", "", i18n.T("passphrase da chave privada SSH (prefira OPF_SSH_KEY_PASSPHRASE)"))
	sshAgent := fs.Bool("ssh-agent", false, i18n.T("autentica via ssh-agent (SSH_AUTH_SOCK ou OpenSSH Agent no Windows)"))
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
	fs.Func("ssh-jump-auth", i18n.T("autenticação da ponte: password, key, key+password ou agent; repetir na ordem de --ssh-jump"), func(value string) error {
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
	var jumpPassphrases []string
	fs.Func("ssh-jump-passphrase", i18n.T("passphrase da chave da ponte (prefira OPF_SSH_JUMP_PASSPHRASES com array JSON)"), func(value string) error {
		jumpPassphrases = append(jumpPassphrases, value)
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
	repoAuthType := fs.String("repo-auth-type", "", i18n.T("autenticação efêmera do repositório adicional: basic ou bearer (credenciais via OPF_REPO_PASSWORD/OPF_REPO_TOKEN ou OPF_REPO_AUTH)"))
	repoAuthUser := fs.String("repo-auth-user", "", i18n.T("usuário Basic Auth do repositório adicional"))

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(i18n.T("argumentos inesperados: %s", strings.Join(fs.Args(), " ")))
	}
	if *showVersion {
		info := version.Current()
		fmt.Printf("opf-cli %s (%s, %s)\n", info.Version, info.Commit, info.ReleaseDate)
		return nil
	}
	if *listProfiles {
		profiles, err := config.LoadProfiles(*profilesFile)
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println(i18n.T("Nenhum perfil salvo."))
			return nil
		}
		for _, p := range profiles {
			targetLabel := p.TargetOS
			if p.TargetVersion != "" {
				targetLabel += ":" + p.TargetVersion
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", p.Name, targetLabel, p.TargetArch, p.SSHHost)
		}
		return nil
	}
	if strings.TrimSpace(*deleteProfileName) != "" {
		if err := config.DeleteProfile(*profilesFile, *deleteProfileName); err != nil {
			return err
		}
		fmt.Println(i18n.T("perfil removido: %s", *deleteProfileName))
		return nil
	}

	setFlags := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	var profileRepos []core.PackageRepository
	if strings.TrimSpace(*profileName) != "" {
		prof, profErr := config.GetProfile(*profilesFile, *profileName)
		if profErr != nil {
			return profErr
		}
		if !setFlags["target"] && prof.TargetOS != "" && prof.TargetVersion != "" {
			*target = prof.TargetOS + ":" + prof.TargetVersion
		}
		if !setFlags["arch"] && prof.TargetArch != "" {
			*arch = prof.TargetArch
		}
		if !setFlags["fetch-method"] && prof.FetchMethod != "" {
			*fetchMethod = prof.FetchMethod
		}
		if !setFlags["container-host"] && prof.ContainerHost != "" {
			*containerHost = prof.ContainerHost
		}
		if !setFlags["image"] && prof.Image != "" {
			*imageOverride = prof.Image
		}
		if !setFlags["wsl-distro"] && prof.WSLDistro != "" {
			*wslDistro = prof.WSLDistro
		}
		if !setFlags["with-optional"] && prof.IncludeOptional {
			*withOptional = true
		}
		if !setFlags["force-reinstall"] && prof.ForceReinstall {
			*forceReinstall = true
		}
		if !setFlags["offline-bundle"] && prof.OfflineBundle {
			*offlineBundle = "auto"
		}
		if !setFlags["ssh-host"] && prof.SSHHost != "" {
			*sshHost = prof.SSHHost
		}
		if !setFlags["ssh-port"] && prof.SSHPort > 0 {
			*sshPort = prof.SSHPort
		}
		if !setFlags["ssh-user"] && prof.SSHUser != "" {
			*sshUser = prof.SSHUser
		}
		if !setFlags["ssh-key"] && prof.SSHKeyPath != "" {
			*sshKey = prof.SSHKeyPath
		}
		if !setFlags["ssh-agent"] && prof.SSHUseAgent {
			*sshAgent = true
		}
		if !setFlags["ssh-known-hosts"] && prof.SSHKnownHosts != "" {
			*sshKnownHosts = prof.SSHKnownHosts
		}
		if !setFlags["remote-dir"] && prof.RemoteDir != "" {
			*remoteDir = prof.RemoteDir
		}
		if len(jumpSpecs) == 0 && len(prof.SSHJumps) > 0 {
			for _, j := range prof.SSHJumps {
				spec := j.Host
				if j.User != "" {
					spec = j.User + "@" + spec
				}
				if j.Port > 0 && j.Port != 22 {
					spec = net.JoinHostPort(j.Host, strconv.Itoa(j.Port))
					if j.User != "" {
						spec = j.User + "@" + spec
					}
				}
				jumpSpecs = append(jumpSpecs, spec)
				mode := j.AuthMode
				if mode == "" {
					mode = opfapp.SSHJumpAuthAgent
				}
				jumpAuths = append(jumpAuths, mode)
				jumpKeys = append(jumpKeys, j.KeyPath)
				jumpKnownHosts = append(jumpKnownHosts, j.KnownHosts)
			}
		}
		if len(packages) == 0 && len(prof.Packages) > 0 {
			packages = append(packages, prof.Packages...)
		}
		profileRepos = prof.Repositories
	}

	if strings.TrimSpace(*sshConfigAlias) != "" {
		entry, cfgErr := transfer.ResolveSSHConfigAlias(*sshConfigFile, *sshConfigAlias)
		if cfgErr != nil {
			return fmt.Errorf("%s: %w", i18n.T("ler ~/.ssh/config"), cfgErr)
		}
		if !setFlags["ssh-host"] && entry.Host != "" {
			*sshHost = entry.Host
		}
		if !setFlags["ssh-port"] && entry.Port > 0 {
			*sshPort = entry.Port
		}
		if !setFlags["ssh-user"] && entry.User != "" {
			*sshUser = entry.User
		}
		if !setFlags["ssh-key"] && entry.KeyPath != "" {
			*sshKey = entry.KeyPath
		}
		if !setFlags["ssh-known-hosts"] && entry.KnownHosts != "" {
			*sshKnownHosts = entry.KnownHosts
		}
		if len(jumpSpecs) == 0 && len(entry.JumpHosts) > 0 {
			for _, j := range entry.JumpHosts {
				spec := j.Host
				if j.Port > 0 && j.Port != 22 {
					spec = net.JoinHostPort(j.Host, strconv.Itoa(j.Port))
				}
				if j.User != "" {
					spec = j.User + "@" + spec
				}
				jumpSpecs = append(jumpSpecs, spec)
				if j.KeyPath != "" {
					jumpAuths = append(jumpAuths, opfapp.SSHJumpAuthKey)
				} else {
					jumpAuths = append(jumpAuths, opfapp.SSHJumpAuthAgent)
				}
				jumpKeys = append(jumpKeys, j.KeyPath)
				jumpKnownHosts = append(jumpKnownHosts, j.KnownHosts)
			}
		}
	}

	if setFlags["resume"] && *resumeID <= 0 {
		return errors.New(i18n.T("--resume exige um ID de sessão positivo"))
	}
	if *pkgFile != "" {
		filePkgs, fileErr := loadPackageFile(*pkgFile)
		if fileErr != nil {
			return fileErr
		}
		packages = append(packages, filePkgs...)
	}
	if *outDir == "" {
		return errors.New(i18n.T("--out não pode ser vazio"))
	}
	if *dbPath == "" {
		return errors.New(i18n.T("--db não pode ser vazio"))
	}
	if *resumeID > 0 {
		resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		meta, metaErr := opfapp.LoadResumeMetadata(resumeCtx, *dbPath, *resumeID)
		resumeCancel()
		if metaErr != nil {
			return fmt.Errorf("%s: %w", i18n.T("carregar sessão %d para retomada", *resumeID), metaErr)
		}
		if strings.TrimSpace(*target) == "" && meta.TargetOS != "" && meta.TargetVersion != "" {
			*target = meta.TargetOS + ":" + meta.TargetVersion
		}
		if strings.TrimSpace(*arch) == "" && meta.TargetArch != "" {
			*arch = meta.TargetArch
		}
		if !setFlags["fetch-method"] && meta.FetchMethod != "" {
			*fetchMethod = meta.FetchMethod
		}
		if strings.TrimSpace(*imageOverride) == "" && meta.Image != "" {
			*imageOverride = meta.Image
		}
		if strings.TrimSpace(*wslDistro) == "" && meta.WSLDistro != "" {
			*wslDistro = meta.WSLDistro
		}
		if strings.TrimSpace(*remoteDir) == "" && meta.RemoteDir != "" {
			*remoteDir = meta.RemoteDir
		}
		if len(packages) == 0 && len(meta.Packages) > 0 {
			packages = append(packages, meta.Packages...)
		}
	}
	if len(packages) == 0 {
		return errors.New(i18n.T("--pkg é obrigatório"))
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
	reportMode, modeErr := core.ParseVersionReportMode(*versionReport)
	if modeErr != nil {
		return modeErr
	}
	if *versionReport == "" && *sshHost == "" {
		reportMode = core.VersionReportNone
	}
	if *resumeID > 0 {
		reportMode = core.VersionReportNone
	}
	if reportMode != core.VersionReportNone && *sshHost == "" {
		return errors.New(i18n.T("--version-report all/default exige --ssh-host"))
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var sshConfig core.SSHConfig
	var sshPasswordValue string
	if *sshHost == "" {
		for _, name := range []string{
			"ssh-port", "ssh-user", "ssh-key", "ssh-key-passphrase", "ssh-agent", "ssh-password",
			"ssh-known-hosts", "remote-dir", "ssh-jump", "ssh-insecure-skip-host-key",
			"ssh-jump-auth", "ssh-jump-password", "ssh-jump-key", "ssh-jump-passphrase", "ssh-jump-known-hosts",
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
		passphrase := *sshKeyPassphrase
		if passphrase == "" {
			passphrase = os.Getenv("OPF_SSH_KEY_PASSPHRASE")
		}
		if *sshKey == "" && password == "" && !*sshAgent {
			return errors.New(i18n.T("informe --ssh-key, --ssh-agent ou --ssh-password/OPF_SSH_PASSWORD"))
		}
		if password != "" {
			logger.Warn(i18n.T("autenticação SSH por senha habilitada; prefira chave privada"))
		}
		sshPasswordValue = password
		sshConfig = core.SSHConfig{
			Host: *sshHost, Port: *sshPort, User: *sshUser,
			KeyPath: *sshKey, KeyPassphrase: passphrase, UseAgent: *sshAgent,
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
		envPassphrases, ppEnvErr := decodeJumpPasswordsEnv(os.Getenv("OPF_SSH_JUMP_PASSPHRASES"))
		if ppEnvErr != nil {
			return errors.New(i18n.T("OPF_SSH_JUMP_PASSPHRASES deve ser um array JSON de strings"))
		}
		jumpPassphraseValues, ppMergeErr := mergeJumpPassphrases(jumpPassphrases, envPassphrases)
		if ppMergeErr != nil {
			return ppMergeErr
		}
		if err := validateJumpFlagCounts(jumpSpecs, jumpAuths, jumpPasswords, jumpKeys, jumpKnownHosts); err != nil {
			return err
		}
		if len(jumpPassphraseValues) > 0 && len(jumpPassphraseValues) != len(jumpSpecs) {
			return errors.New(i18n.T("OPF_SSH_JUMP_PASSPHRASES tem %d valores para %d pontes", len(jumpPassphraseValues), len(jumpSpecs)))
		}
		if len(envPasswords) > 0 && len(envPasswords) != len(jumpSpecs) {
			return errors.New(i18n.T("OPF_SSH_JUMP_PASSWORDS tem %d valores para %d pontes", len(envPasswords), len(jumpSpecs)))
		}
		jumps, jumpErr := buildJumpConfigsWithPassphrases(sshConfig, jumpSpecs, jumpAuths, jumpPasswordValues, jumpKeys, jumpPassphraseValues, jumpKnownHosts)
		if jumpErr != nil {
			return jumpErr
		}
		sshConfig.JumpHosts = jumps
		if len(jumps) > 0 {
			logger.Warn(i18n.T("pontes SSH configuradas; credenciais ficam apenas em memória"))
		}
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
	if method != "docker" && method != "podman" && method != "wsl" {
		return errors.New(i18n.T("--fetch-method inválido: %q (use docker, podman ou wsl)", *fetchMethod))
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

	proxyCfg := buildProxyConfig(*httpProxy, *httpsProxy, *noProxy, *caCert, *inheritProxy)
	if err := proxyCfg.Validate(false); err != nil {
		return err
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
		authType:      *repoAuthType,
		authUser:      *repoAuthUser,
	}, family, setFlags)
	if err != nil {
		return err
	}
	if len(repositories) == 0 && len(profileRepos) > 0 {
		repositories = profileRepos
	}
	repositories, err = applyRepoAuthEnv(repositories, os.Getenv("OPF_REPO_AUTH"))
	if err != nil {
		return err
	}
	if len(repositories) > 0 {
		repositories, err = core.NormalizePackageRepositories(repositories, family)
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(*saveProfileName) != "" {
		prof := config.Profile{
			Name:            *saveProfileName,
			TargetOS:        targetOS,
			TargetVersion:   targetVersion,
			TargetArch:      strings.ToLower(*arch),
			FetchMethod:     method,
			ContainerHost:   strings.TrimSpace(*containerHost),
			Image:           strings.TrimSpace(*imageOverride),
			WSLDistro:       strings.TrimSpace(*wslDistro),
			IncludeOptional: *withOptional,
			ForceReinstall:  *forceReinstall,
			OfflineBundle:   strings.TrimSpace(*offlineBundle) != "",
			SSHHost:         sshConfig.Host,
			SSHPort:         sshConfig.Port,
			SSHUser:         sshConfig.User,
			SSHKeyPath:      sshConfig.KeyPath,
			SSHUseAgent:     sshConfig.UseAgent,
			SSHKnownHosts:   sshConfig.KnownHosts,
			RemoteDir:       strings.TrimSpace(*remoteDir),
			Repositories:    repositories,
			Packages:        packages,
		}
		for _, jm := range opfapp.SSHJumpMetadataFromConfigs(sshConfig.JumpHosts) {
			prof.SSHJumps = append(prof.SSHJumps, config.ProfileJump{
				Host: jm.Host, Port: jm.Port, User: jm.User,
				AuthMode: jm.AuthMode, KeyPath: jm.KeyPath, KnownHosts: jm.KnownHosts,
			})
		}
		if err := config.UpsertProfile(*profilesFile, prof); err != nil {
			return err
		}
		fmt.Println(i18n.T("perfil salvo: %s", prof.Name))
	}

	if *useSudo && sshPasswordValue == "" {
		return errors.New(i18n.T("--sudo exige --ssh-password/OPF_SSH_PASSWORD para autenticar o sudo com a senha do usuário SSH"))
	}
	var confirmInstall core.InstallConfirmation
	if *installAfter {
		confirmInstall = func(_ context.Context, artifacts []core.PackageArtifact) (bool, error) {
			return confirmInstallation(os.Stdin, os.Stdout, len(artifacts), *sshUser+"@"+*sshHost, *assumeYes)
		}
	}

	svc, err := opfapp.New(opfapp.Config{
		StorePath:         *dbPath,
		OutputBaseDir:     *outDir,
		TempDir:           *tempDir,
		TargetOS:          targetOS,
		TargetVersion:     targetVersion,
		TargetArch:        strings.ToLower(*arch),
		Packages:          packages,
		IncludeOptional:   *withOptional,
		FetchMethod:       method,
		ContainerHost:     strings.TrimSpace(*containerHost),
		Image:             strings.TrimSpace(*imageOverride),
		WSLDistro:         *wslDistro,
		Proxy:             proxyCfg,
		OfflineBundlePath: strings.TrimSpace(*offlineBundle),
		SSH:               sshConfig,
		RemoteDir:         *remoteDir,
		Repositories:      repositories,
		ForceReinstall:    *forceReinstall,
		VersionReport:     reportMode,
		Logger:            logger,
		Install:           *installAfter,
		UseSudo:           *useSudo,
		SudoPassword:      sshPasswordValue,
		ConfirmInstall:    confirmInstall,
		SyslogEnabled:     *syslogEnabled,
		SyslogHost:        *syslogHost,
		SyslogPort:        *syslogPort,
		SyslogProto:       *syslogProto,
		SyslogTag:         *syslogTag,
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var result *core.PipelineResult
	switch {
	case *resumeID > 0:
		result, err = svc.Resume(ctx, *resumeID)
	case reportMode != core.VersionReportNone:
		preview, previewErr := svc.PreviewPackageDiff(ctx)
		if previewErr != nil {
			return previewErr
		}
		if preview != nil {
			printPackagePreview(preview)
			if len(preview.Data.Diff) > 0 && *installAfter && !*assumeYes {
				if !confirmPackageDiff(os.Stdin, os.Stdout, len(preview.Data.Diff)) {
					if abortErr := svc.AbortPreview(ctx, preview); abortErr != nil {
						return abortErr
					}
					fmt.Println(i18n.T("instalação recusada; sessão abortada (relatório disponível)"))
					if *exportJSON != "" || *exportMarkdown != "" {
						if reportErr := exportSessionReport(ctx, svc, preview.SessionID, *exportJSON, *exportMarkdown); reportErr != nil {
							return reportErr
						}
					}
					return nil
				}
			}
			result, err = svc.RunPreviewed(ctx, preview, nil)
		} else {
			result, err = svc.Run(ctx)
		}
	default:
		result, err = svc.Run(ctx)
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", i18n.T("sessão %d concluída", result.SessaoID))
	if result.AllAlreadyInstalled {
		fmt.Println(i18n.T("todos os pacotes já estão instalados no destino"))
	}
	fmt.Printf("%s\n", i18n.T("artefatos: %d", len(result.Artifacts)))
	fmt.Printf("%s\n", i18n.T("saída: %s", result.OutputDir))
	fmt.Printf("%s\n", i18n.T("manifesto: %s", result.CaminhoManifesto))
	if result.OfflineBundlePath != "" {
		fmt.Printf("%s\n", i18n.T("bundle offline: %s", result.OfflineBundlePath))
	}
	if result.RemoteDir != "" {
		fmt.Printf("%s\n", i18n.T("destino remoto: %s", result.RemoteDir))
	}
	if *exportJSON != "" || *exportMarkdown != "" {
		if reportErr := exportSessionReport(ctx, svc, result.SessaoID, *exportJSON, *exportMarkdown); reportErr != nil {
			return fmt.Errorf("%s: %w", i18n.T("gerar relatório"), reportErr)
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
	authType      string
	authUser      string
}

func (f repositoryFlags) simpleSet(setFlags map[string]bool) bool {
	for _, name := range []string{
		"repo-url", "repo-name", "repo-family", "repo-key", "repo-key-url",
		"repo-ca", "repo-apt-suite", "repo-apt-components", "repo-apt-flat",
		"repo-insecure-tls", "repo-insecure-gpg", "repo-auth-type", "repo-auth-user",
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
		AuthType:              flags.authType,
		AuthUsername:          flags.authUser,
		AuthPassword:          os.Getenv("OPF_REPO_PASSWORD"),
		AuthToken:             os.Getenv("OPF_REPO_TOKEN"),
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
	return buildJumpConfigsWithPassphrases(base, specs, auths, passwords, keys, nil, knownHosts)
}

func buildJumpConfigsWithPassphrases(base core.SSHConfig, specs, auths, passwords, keys, passphrases, knownHosts []string) ([]core.SSHConfig, error) {
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
			Host:          host,
			Port:          port,
			User:          user,
			AuthMode:      jumpValueAt(auths, i),
			Password:      jumpValueAt(passwords, i),
			KeyPath:       jumpValueAt(keys, i),
			KeyPassphrase: jumpValueAt(passphrases, i),
			KnownHosts:    jumpValueAt(knownHosts, i),
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

func mergeJumpPassphrases(flagValues, envValues []string) ([]string, error) {
	if len(flagValues) > 0 && len(envValues) > 0 {
		return nil, errors.New(i18n.T("use --ssh-jump-passphrase ou OPF_SSH_JUMP_PASSPHRASES, não ambos"))
	}
	if len(envValues) > 0 {
		return envValues, nil
	}
	return flagValues, nil
}

func buildProxyConfig(httpProxy, httpsProxy, noProxy, caCert string, inherit bool) core.ProxyConfig {
	cfg := core.ProxyConfig{
		HTTPProxy:  strings.TrimSpace(httpProxy),
		HTTPSProxy: strings.TrimSpace(httpsProxy),
		NoProxy:    strings.TrimSpace(noProxy),
		CACertFile: strings.TrimSpace(caCert),
	}
	if inherit {
		if cfg.HTTPProxy == "" {
			cfg.HTTPProxy = firstNonEmptyEnv("HTTP_PROXY", "http_proxy")
		}
		if cfg.HTTPSProxy == "" {
			cfg.HTTPSProxy = firstNonEmptyEnv("HTTPS_PROXY", "https_proxy")
		}
		if cfg.NoProxy == "" {
			cfg.NoProxy = firstNonEmptyEnv("NO_PROXY", "no_proxy")
		}
	}
	return cfg
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

type repoAuthEntry struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

func applyRepoAuthEnv(repos []core.PackageRepository, raw string) ([]core.PackageRepository, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(repos) == 0 {
		return repos, nil
	}
	var entries map[string]repoAuthEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, errors.New(i18n.T("OPF_REPO_AUTH deve ser um objeto JSON mapeando nome do repositório para credenciais"))
	}
	out := append([]core.PackageRepository(nil), repos...)
	for i := range out {
		auth, ok := entries[out[i].Name]
		if !ok {
			auth, ok = entries[core.RepositorySlug(out[i].Name)]
		}
		if !ok {
			continue
		}
		out[i].AuthType = auth.Type
		out[i].AuthUsername = auth.Username
		out[i].AuthPassword = auth.Password
		out[i].AuthToken = auth.Token
	}
	return out, nil
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

func printPackagePreview(preview *opfapp.PackagePreview) {
	if preview == nil {
		return
	}
	if len(preview.Data.Inventory) > 0 {
		fmt.Println(i18n.T("Pacotes instalados coletados: %d", len(preview.Data.Inventory)))
	}
	if len(preview.Data.Diff) == 0 {
		fmt.Println(i18n.T("Nenhuma atualização de pacote detectada."))
		return
	}
	fmt.Println(i18n.T("Pacotes que serão atualizados:"))
	for _, diff := range preview.Data.Diff {
		role := i18n.T("principal")
		if diff.IsDependency {
			role = i18n.T("dependência")
		}
		fmt.Printf("  %s: %s → %s (%s)\n", diff.Name, diff.From, diff.To, role)
	}
}

const packageFileLimit = 1 << 20

func loadPackageFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("ler --pkg-file"), err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New(i18n.T("--pkg-file deve apontar para um arquivo regular"))
	}
	if info.Size() > packageFileLimit {
		return nil, errors.New(i18n.T("--pkg-file excede %d bytes", packageFileLimit))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("ler --pkg-file"), err)
	}
	var out []string
	for _, rawLine := range strings.Split(string(data), "\n") {
		if idx := strings.IndexByte(rawLine, '#'); idx >= 0 {
			rawLine = rawLine[:idx]
		}
		for _, item := range strings.Split(rawLine, ",") {
			if pkg := strings.TrimSpace(item); pkg != "" {
				out = append(out, pkg)
			}
		}
	}
	return out, nil
}

func exportSessionReport(ctx context.Context, svc *opfapp.Service, sessionID int64, jsonPath, mdPath string) error {
	relatorio, err := svc.GenerateReport(ctx, sessionID)
	if err != nil {
		return err
	}
	if jsonPath != "" {
		if err := observability.SalvarJSON(jsonPath, relatorio); err != nil {
			return err
		}
		fmt.Println(i18n.T("relatório JSON: %s", jsonPath))
	}
	if mdPath != "" {
		if err := observability.SalvarMarkdown(mdPath, relatorio); err != nil {
			return err
		}
		fmt.Println(i18n.T("relatório Markdown: %s", mdPath))
	}
	return nil
}

func confirmPackageDiff(in io.Reader, out io.Writer, count int) bool {
	fmt.Fprintf(out, i18n.T("%d pacote(s) serão atualizados. Prosseguir com a instalação? [s/N]: "), count)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "s", "sim", "y", "yes":
		return true
	default:
		return false
	}
}
