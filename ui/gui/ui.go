//go:build cgo

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	opfapp "opf/internal/app"
	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/store"
)

type UI struct {
	fyneApp      fyne.App
	window       fyne.Window
	store        *store.Store
	settings     config.Settings
	settingsPath string
	settingsErr  error
	themeMode    string
	screenTitle  string

	running         bool
	cancel          context.CancelFunc
	resumeID        int64
	sessionID       int64
	reportSessionID int64
	reportBack      func()
	result          *core.PipelineResult
	runErr          error
	reporter        *guiReporter

	stepLabels    []*widget.Label
	progressBar   *widget.ProgressBar
	progressText  *widget.Label
	logLabel      *widget.Label
	testStatus    *widget.Label
	detectStatus  *widget.Label
	detected      *opfapp.TargetInfo
	detectedHost  string
	detectedUser  string
	detectedPort  string
	detectedJumps string
	manualTarget  bool

	targetSelect  *widget.Select
	versionEntry  *widget.Entry
	archEntry     *widget.Entry
	methodSelect  *widget.Select
	imageEntry    *widget.Entry
	wslEntry      *widget.Entry
	optionalCheck *widget.Check
	forceCheck    *widget.Check
	pkgEntry      *widget.Entry

	sshEnabled  *widget.Check
	sshHost     *widget.Entry
	sshPort     *widget.Entry
	sshUser     *widget.Entry
	sshKey      *widget.Entry
	sshPassword *widget.Entry
	skipHostKey *widget.Check
	knownHosts  *widget.Entry
	jumpRows    []*jumpRow
	jumpList    *fyne.Container
	repoRows    []*repoRow
	repoList    *fyne.Container
	remoteDir   *widget.Entry

	installCheck *widget.Check
	sudoCheck    *widget.Check
	sudoPassword *widget.Entry

	outEntry        *widget.Entry
	dbEntry         *widget.Entry
	settingsTemp    *widget.Entry
	settingsTimeout *widget.Entry
	syslogCheck     *widget.Check
	syslogHost      *widget.Entry
	syslogPort      *widget.Entry
	syslogProto     *widget.Entry
	syslogTag       *widget.Entry
}

func Run() error {
	a := app.NewWithID("io.opf.gui")
	themeMode := a.Preferences().StringWithFallback("theme", "system")
	applyThemeMode(a, themeMode)
	a.SetIcon(appIcon)
	window := a.NewWindow("OPF — Offline Package Fetcher")
	window.SetIcon(appIcon)
	window.Resize(fyne.NewSize(1240, 860))
	window.CenterOnScreen()
	ui := newUI(a, window)
	window.SetCloseIntercept(func() {
		if ui.running && ui.cancel != nil {
			ui.cancel()
		}
		ui.closeStore()
		a.Quit()
	})
	ui.showWelcome()
	window.ShowAndRun()
	ui.closeStore()
	return nil
}

func newUI(a fyne.App, window fyne.Window) *UI {
	settingsPath, _ := config.DefaultPath()
	settings, settingsErr := config.Load(settingsPath)
	if settingsErr != nil {
		settings = config.Default()
	}

	ui := &UI{
		fyneApp:      a,
		window:       window,
		settings:     settings,
		settingsPath: settingsPath,
		settingsErr:  settingsErr,
		themeMode:    a.Preferences().StringWithFallback("theme", "system"),
	}

	ui.targetSelect = widget.NewSelect([]string{"Ubuntu", "Debian", "CentOS", "Rocky Linux", "AlmaLinux", "Fedora", "RHEL", "Alpine"}, func(selected string) {
		if ui.versionEntry == nil || ui.archEntry == nil {
			return
		}
		switch selected {
		case "Debian":
			ui.versionEntry.SetText("12")
			ui.archEntry.SetText("amd64")
		case "CentOS":
			ui.versionEntry.SetText("7")
			ui.archEntry.SetText("x86_64")
		case "Rocky Linux", "AlmaLinux":
			ui.versionEntry.SetText("9")
			ui.archEntry.SetText("x86_64")
		case "Fedora":
			ui.versionEntry.SetText("41")
			ui.archEntry.SetText("x86_64")
		case "RHEL":
			ui.versionEntry.SetText("9")
			ui.archEntry.SetText("x86_64")
		case "Alpine":
			ui.versionEntry.SetText("3.20")
			ui.archEntry.SetText("x86_64")
		default:
			ui.versionEntry.SetText("22.04")
			ui.archEntry.SetText("amd64")
		}
		if ui.repoList != nil {
			ui.refreshRepoList()
		}
	})
	ui.targetSelect.SetSelected("Ubuntu")
	ui.versionEntry = widget.NewEntry()
	ui.versionEntry.SetText("22.04")
	ui.archEntry = widget.NewEntry()
	ui.archEntry.SetText("amd64")
	ui.methodSelect = widget.NewSelect([]string{"Docker", "WSL", "Automático"}, nil)
	ui.methodSelect.SetSelected("Docker")
	ui.imageEntry = widget.NewEntry()
	ui.imageEntry.SetPlaceHolder("opcional: opf/centos-7:f3")
	ui.wslEntry = widget.NewEntry()
	ui.wslEntry.SetPlaceHolder("ex.: ubuntu-24.04")
	ui.optionalCheck = widget.NewCheck("Incluir dependências opcionais", nil)
	ui.forceCheck = widget.NewCheck("Forçar reinstalação (ignorar pré-check)", nil)
	ui.pkgEntry = widget.NewMultiLineEntry()
	ui.pkgEntry.SetPlaceHolder("Um pacote por linha ou separado por vírgula. Ex.: vim, curl")
	ui.pkgEntry.SetMinRowsVisible(4)

	ui.sshEnabled = widget.NewCheck("Enviar e instalar via SSH", nil)
	ui.sshHost = widget.NewEntry()
	ui.sshHost.SetPlaceHolder("host ou IP do destino")
	ui.sshPort = widget.NewEntry()
	ui.sshPort.SetText("22")
	ui.sshUser = widget.NewEntry()
	ui.sshUser.SetPlaceHolder("usuário")
	ui.sshKey = widget.NewEntry()
	ui.sshKey.SetPlaceHolder("~/.ssh/id_ed25519 (opcional)")
	ui.sshPassword = widget.NewPasswordEntry()
	ui.sshPassword.SetPlaceHolder("senha SSH/conta (também usada pelo sudo)")
	ui.skipHostKey = widget.NewCheck("Ignorar verificação de host key (inseguro)", nil)
	ui.knownHosts = widget.NewEntry()
	ui.knownHosts.SetPlaceHolder("~/.ssh/known_hosts")
	ui.remoteDir = widget.NewEntry()
	ui.remoteDir.SetPlaceHolder("/tmp/opf-sessao (padrão)")

	ui.installCheck = widget.NewCheck("Instalar após transferir e validar", nil)
	ui.sudoCheck = widget.NewCheck("Usar sudo com a senha da conta", nil)
	ui.sudoPassword = widget.NewPasswordEntry()
	ui.sudoPassword.SetPlaceHolder("senha da conta para sudo")

	cwd, _ := os.Getwd()
	ui.outEntry = widget.NewEntry()
	ui.outEntry.SetText(filepath.Join(cwd, "opf-output"))
	ui.dbEntry = widget.NewEntry()
	dbPath := strings.TrimSpace(settings.DatabasePath)
	if dbPath == "" {
		dbPath = filepath.Join(cwd, "opf.db")
	}
	ui.dbEntry.SetText(dbPath)

	ui.settingsTemp = widget.NewEntry()
	ui.settingsTemp.SetText(settings.TempDir)
	ui.settingsTimeout = widget.NewEntry()
	ui.settingsTimeout.SetText(strconv.Itoa(settings.StageTimeoutSecs))
	ui.syslogCheck = widget.NewCheck("Habilitar envio ao Syslog externo", nil)
	ui.syslogCheck.SetChecked(settings.SyslogEnabled)
	ui.syslogHost = widget.NewEntry()
	ui.syslogHost.SetText(settings.SyslogHost)
	ui.syslogPort = widget.NewEntry()
	port := settings.SyslogPort
	if port == 0 {
		port = 514
	}
	ui.syslogPort.SetText(strconv.Itoa(port))
	ui.syslogProto = widget.NewEntry()
	proto := settings.SyslogProto
	if proto == "" {
		proto = "udp"
	}
	ui.syslogProto.SetText(proto)
	ui.syslogTag = widget.NewEntry()
	tag := settings.SyslogTag
	if tag == "" {
		tag = "opf"
	}
	ui.syslogTag.SetText(tag)

	ui.progressBar = widget.NewProgressBar()
	ui.progressText = widget.NewLabel("Aguardando início")
	ui.progressText.Wrapping = fyne.TextWrapWord
	ui.logLabel = widget.NewLabel("")
	ui.logLabel.Wrapping = fyne.TextWrapWord
	ui.testStatus = widget.NewLabel("Teste de conexão: não executado")
	ui.detectStatus = widget.NewLabel("Destino não detectado")

	ui.reporter = &guiReporter{ui: ui}
	ui.openStore()
	return ui
}

func (u *UI) openStore() {
	if u.store != nil {
		_ = u.store.Close()
		u.store = nil
	}
	path := strings.TrimSpace(u.dbEntry.Text)
	if path == "" {
		path = "opf.db"
	}
	st, err := store.Open(path)
	if err != nil {
		u.settingsErr = fmt.Errorf("abrir SQLite %q: %w", path, err)
		return
	}
	u.store = st
	u.settingsErr = nil
}

func (u *UI) closeStore() {
	if u.store != nil {
		_ = u.store.Close()
		u.store = nil
	}
}

func (u *UI) targetOS() string {
	switch u.targetSelect.Selected {
	case "Debian":
		return "debian"
	case "CentOS":
		return "centos"
	case "Rocky Linux":
		return "rockylinux"
	case "AlmaLinux":
		return "almalinux"
	case "Fedora":
		return "fedora"
	case "RHEL":
		return "rhel"
	case "Alpine":
		return "alpine"
	default:
		return "ubuntu"
	}
}

func (u *UI) targetFamily() (string, error) {
	return opfapp.TargetFamily(u.targetOS())
}

func (u *UI) fetchMethod() string {
	switch u.methodSelect.Selected {
	case "WSL":
		return "wsl"
	case "Automático":
		return "auto"
	default:
		return "docker"
	}
}

func (u *UI) packages() []string {
	value := strings.ReplaceAll(u.pkgEntry.Text, ",", "\n")
	seen := make(map[string]struct{})
	var packages []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		packages = append(packages, line)
	}
	return packages
}

func (u *UI) showError(err error) {
	if err == nil {
		return
	}
	dialog.ShowError(err, u.window)
}

func (u *UI) setBusy(busy bool) {
	u.running = busy
}

func (u *UI) appendLog(line string) {
	text := u.logLabel.Text
	lines := strings.Split(text, "\n")
	lines = append(lines, line)
	if len(lines) > 400 {
		lines = lines[len(lines)-400:]
	}
	u.logLabel.SetText(strings.Join(lines, "\n"))
}

func (u *UI) markStep(step int, status string) {
	if step < 1 || step > len(u.stepLabels) {
		return
	}
	u.stepLabels[step-1].SetText(fmt.Sprintf("%s %02d. %s", status, step, core.NomeEtapa(step)))
}

func (u *UI) resetSteps() {
	u.stepLabels = make([]*widget.Label, core.TotalEtapas)
	for i := 1; i <= core.TotalEtapas; i++ {
		u.stepLabels[i-1] = widget.NewLabel(fmt.Sprintf("[ ] %02d. %s", i, core.NomeEtapa(i)))
	}
}

func (u *UI) currentPassword() string {
	if u.sshPassword == nil {
		return ""
	}
	return u.sshPassword.Text
}

func (u *UI) currentSudoPassword() string {
	if u.sudoCheck != nil && u.sudoCheck.Checked {
		if strings.TrimSpace(u.sudoPassword.Text) != "" {
			return u.sudoPassword.Text
		}
		if strings.TrimSpace(u.sshPassword.Text) != "" {
			return u.sshPassword.Text
		}
	}
	return ""
}

func (u *UI) statusText() string {
	db := "SQLite: OK"
	if u.store == nil {
		db = "SQLite: indisponível"
	}
	text := db + "  •  Docker: verificado ao iniciar  •  WSL: disponível apenas no Windows"
	if u.settingsErr != nil {
		text += "\n" + u.settingsErr.Error()
	}
	return text
}
