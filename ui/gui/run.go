//go:build cgo

package gui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"

	"opf/internal/app"
	"opf/internal/core"
	"opf/internal/i18n"
	"opf/internal/transfer"
)

// initFyneTranslations fills the Fyne built-in strings that are missing from
// the bundled pt-BR and es translations, so Fyne never logs a translation
// failure when a select widget is rendered without a value.
func initFyneTranslations() {
	_ = lang.AddTranslationsForLocale([]byte(`{"(Select one)":"(Selecione um)"}`), fyne.Locale("pt-BR"))
	_ = lang.AddTranslationsForLocale([]byte(`{"(Select one)":"(Seleccione uno)"}`), fyne.Locale("es"))
}

func (u *UI) startRun() {
	packages := u.packages()
	if len(packages) == 0 {
		u.showError(fmt.Errorf("%s", i18n.T("informe ao menos um pacote")))
		return
	}
	cfg, err := u.buildConfig(packages)
	if err != nil {
		u.showError(err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.running = true
	u.result = nil
	u.runErr = nil
	u.showProgress()

	resumeID := u.resumeID
	go func() {
		if cfg.Install && cfg.SSH.Host != "" {
			isRoot, probeErr := app.DetectRoot(ctx, cfg.SSH)
			if probeErr != nil {
				fyne.Do(func() { u.finishRun(nil, fmt.Errorf("%s: %w", i18n.T("detectar privilégios remotos"), probeErr)) })
				return
			}
			if isRoot {
				cfg.UseSudo = false
				cfg.SudoPassword = ""
			} else if !cfg.UseSudo {
				fyne.Do(func() {
					u.finishRun(nil, errors.New(i18n.T("usuário remoto não é root; habilite “Usar sudo” e informe a senha da conta")))
				})
				return
			}
		}
		svc, createErr := app.New(cfg)
		if createErr != nil {
			fyne.Do(func() { u.finishRun(nil, createErr) })
			return
		}
		defer svc.Close()

		var result *core.PipelineResult
		if resumeID > 0 {
			result, createErr = svc.Resume(ctx, resumeID)
		} else {
			result, createErr = svc.Run(ctx)
		}
		fyne.Do(func() { u.finishRun(result, createErr) })
	}()
}

func (u *UI) buildConfig(packages []string) (app.Config, error) {
	if strings.TrimSpace(u.outEntry.Text) == "" || strings.TrimSpace(u.dbEntry.Text) == "" {
		return app.Config{}, errors.New(i18n.T("diretórios de saída e banco são obrigatórios"))
	}
	var sshCfg core.SSHConfig
	if u.sshEnabled.Checked {
		var err error
		sshCfg, err = u.buildSSHConfig()
		if err != nil {
			return app.Config{}, err
		}
	}

	repositories, err := u.buildRepositories()
	if err != nil {
		return app.Config{}, err
	}

	install := u.sshEnabled.Checked && u.installCheck.Checked
	sudoPassword := u.currentSudoPassword()
	if install && u.sudoCheck.Checked && sudoPassword == "" {
		return app.Config{}, errors.New(i18n.T("sudo exige a senha da conta"))
	}

	cfg := app.Config{
		StorePath:       strings.TrimSpace(u.dbEntry.Text),
		OutputBaseDir:   strings.TrimSpace(u.outEntry.Text),
		TempDir:         strings.TrimSpace(u.settings.TempDir),
		TargetOS:        u.targetOS(),
		TargetVersion:   strings.TrimSpace(u.versionEntry.Text),
		TargetArch:      strings.TrimSpace(u.archEntry.Text),
		Packages:        packages,
		IncludeOptional: u.optionalCheck.Checked,
		FetchMethod:     u.fetchMethod(),
		Image:           strings.TrimSpace(u.imageEntry.Text),
		WSLDistro:       strings.TrimSpace(u.wslEntry.Text),
		SSH:             sshCfg,
		RemoteDir:       strings.TrimSpace(u.remoteDir.Text),
		Repositories:    repositories,
		ForceReinstall:  u.forceCheck.Checked,
		StageTimeout:    u.settings.StageTimeout(),
		Reporter:        u.reporter,
		Install:         install,
		UseSudo:         install && u.sudoCheck.Checked,
		SudoPassword:    sudoPassword,
		SyslogEnabled:   u.settings.SyslogEnabled,
		SyslogHost:      u.settings.SyslogHost,
		SyslogPort:      u.settings.SyslogPort,
		SyslogProto:     u.settings.SyslogProto,
		SyslogTag:       u.settings.SyslogTag,
	}
	if install {
		cfg.ConfirmInstall = u.confirmInstall
	}
	if !u.sshEnabled.Checked {
		cfg.SSH = core.SSHConfig{}
	}
	return cfg, nil
}

func (u *UI) buildSSHConfig() (core.SSHConfig, error) {
	port := 22
	if strings.TrimSpace(u.sshPort.Text) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(u.sshPort.Text))
		if err != nil || parsed <= 0 || parsed > 65535 {
			return core.SSHConfig{}, errors.New(i18n.T("porta SSH inválida"))
		}
		port = parsed
	}
	cfg := core.SSHConfig{
		Host:                strings.TrimSpace(u.sshHost.Text),
		Port:                port,
		User:                strings.TrimSpace(u.sshUser.Text),
		KeyPath:             strings.TrimSpace(u.sshKey.Text),
		Password:            u.sshPassword.Text,
		KnownHosts:          strings.TrimSpace(u.knownHosts.Text),
		InsecureSkipHostKey: u.skipHostKey.Checked,
	}
	if cfg.Host == "" || cfg.User == "" {
		return cfg, errors.New(i18n.T("host e usuário SSH são obrigatórios"))
	}
	if cfg.KeyPath == "" && cfg.Password == "" {
		return cfg, errors.New(i18n.T("informe chave SSH ou senha da conta"))
	}
	jumps, err := u.buildJumpConfigs(cfg)
	if err != nil {
		return cfg, err
	}
	cfg.JumpHosts = jumps
	return cfg, nil
}

func (u *UI) startNewSession() {
	u.runErr = nil
	u.result = nil
	u.resumeID = 0
	u.detected = nil
	u.detectedHost = ""
	u.detectedUser = ""
	u.detectedPort = ""
	u.detectedJumps = ""
	u.manualTarget = false
	u.clearJumpRows()
	u.clearRepoRows()
	u.showSSH()
}

func (u *UI) detectedMatchesSSH() bool {
	return u.detectedHost == u.sshHost.Text && u.detectedUser == u.sshUser.Text &&
		u.detectedPort == u.sshPort.Text && u.detectedJumps == u.jumpFingerprint()
}

func (u *UI) detectTarget() {
	cfg, err := u.buildSSHConfig()
	if err != nil {
		u.showError(err)
		return
	}
	u.detectStatus.SetText(i18n.T("Detectando..."))
	detectedHost := u.sshHost.Text
	detectedUser := u.sshUser.Text
	detectedPort := u.sshPort.Text
	detectedJumps := u.jumpFingerprint()
	go func() {
		info, detectErr := app.DetectRemoteTarget(context.Background(), cfg)
		fyne.Do(func() {
			if detectErr != nil {
				u.detectStatus.SetText(i18n.T("[✘] Detecção falhou"))
				u.showError(detectErr)
				return
			}
			u.detected = info
			u.detectedHost = detectedHost
			u.detectedUser = detectedUser
			u.detectedPort = detectedPort
			u.detectedJumps = detectedJumps
			u.manualTarget = false
			switch info.OS {
			case "debian":
				u.targetSelect.SetSelected("Debian")
			case "centos":
				u.targetSelect.SetSelected("CentOS")
			case "rockylinux":
				u.targetSelect.SetSelected("Rocky Linux")
			case "almalinux":
				u.targetSelect.SetSelected("AlmaLinux")
			case "fedora":
				u.targetSelect.SetSelected("Fedora")
			case "rhel":
				u.targetSelect.SetSelected("RHEL")
			case "alpine":
				u.targetSelect.SetSelected("Alpine")
			default:
				u.targetSelect.SetSelected("Ubuntu")
			}
			u.versionEntry.SetText(info.Version)
			u.archEntry.SetText(info.Arch)
			u.imageEntry.SetText(info.Image)
			status := i18n.T("[✔] %s %s (%s) • família %s", info.OS, info.Version, info.Arch, info.Family)
			if !info.Tested {
				status += i18n.T(" • preset não validado; revise a configuração avançada")
			}
			u.detectStatus.SetText(status)
		})
	}()
}

func (u *UI) finishRun(result *core.PipelineResult, err error) {
	u.running = false
	u.cancel = nil
	u.result = result
	u.runErr = err
	if result != nil {
		u.sessionID = result.SessaoID
		u.resumeID = 0
	}
	u.openStore()
	u.showResult()
}

func (u *UI) cancelRun() {
	if u.cancel == nil {
		return
	}
	u.reporter.Log("warn", i18n.T("cancelamento solicitado"))
	u.cancel()
}

func (u *UI) confirmInstall(ctx context.Context, artifacts []core.PackageArtifact) (bool, error) {
	result := make(chan bool, 1)
	fyne.Do(func() {
		d := dialog.NewConfirm(
			i18n.T("Confirmar instalação"),
			i18n.T("Instalar %d artefatos no destino?\nA operação usa sudo quando necessário e pode ser interrompida de forma best-effort.", len(artifacts)),
			func(ok bool) {
				select {
				case result <- ok:
				default:
				}
			},
			u.window,
		)
		d.SetConfirmText(i18n.T("Instalar"))
		d.SetDismissText(i18n.T("Recusar"))
		d.Show()
	})
	select {
	case ok := <-result:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (u *UI) testSSH() {
	if !u.sshEnabled.Checked {
		dialog.ShowInformation("SSH", i18n.T("A transferência SSH está desativada."), u.window)
		return
	}
	cfg, err := u.buildSSHConfig()
	if err != nil {
		u.showError(err)
		return
	}

	u.testStatus.SetText(i18n.T("Teste de conexão: testando..."))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		manager := transfer.NewSSHTransferManager()
		err := manager.Connect(ctx, cfg)
		if err == nil {
			_ = manager.Close()
		}
		fyne.Do(func() {
			if err != nil {
				u.testStatus.SetText(i18n.T("Teste de conexão: falhou"))
				u.showError(err)
				return
			}
			u.testStatus.SetText(i18n.T("Teste de conexão: conectado"))
		})
	}()
}
