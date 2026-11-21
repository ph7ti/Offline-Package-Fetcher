package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/i18n"
	"opf/internal/store"
)

type screen int

const (
	screenWelcome screen = iota
	screenConfig
	screenRepos
	screenRepoEdit
	screenSSH
	screenJumps
	screenJumpEdit
	screenPackages
	screenProgress
	screenResult
	screenReport
	screenHistory
	screenSettings
)

type formItem struct {
	label         string
	input         *textinput.Model
	toggle        *bool
	choice        *string
	choices       []string
	choiceDisplay func(string) string
}

type confirmRequest struct {
	count    int
	response chan bool
}

type pipelineStepMsg struct {
	step   int
	status string
	name   string
}

type pipelineProgressMsg struct {
	stage   string
	current int64
	total   int64
}

type pipelineLogMsg struct {
	level   string
	message string
}

type pipelineDoneMsg struct {
	result *core.PipelineResult
	err    error
}

type sshTestMsg struct {
	err error
}

type Model struct {
	screen    screen
	width     int
	height    int
	menuIdx   int
	sessionID int64
	resumeID  int64
	errorMsg  string

	settingsPath string
	settings     config.Settings
	store        *store.Store

	steps        [core.TotalEtapas]string
	stepName     [core.TotalEtapas]string
	progress     float64
	progressText string
	logs         []string
	running      bool
	cancel       context.CancelFunc
	confirm      *confirmRequest

	result          *core.PipelineResult
	runErr          error
	reportText      string
	reportOffset    int
	reportBack      screen
	reportSessionID int64

	sessions   []core.SessaoResumo
	sessionIdx int

	form  []formItem
	focus int

	optional      bool
	force         bool
	syslogEnabled bool
	skipHostKey   bool
	detectedText  string
	detectedHost  string
	detectedUser  string
	detectedPort  string
	detectedJumps string
	manualTarget  bool
	sshEnabled    bool
	install       bool
	sudo          bool

	targetInput  *textinput.Model
	versionInput *textinput.Model
	archInput    *textinput.Model
	methodInput  *textinput.Model
	imageInput   *textinput.Model
	wslInput     *textinput.Model
	outInput     *textinput.Model
	dbInput      *textinput.Model

	sshHost     *textinput.Model
	sshPort     *textinput.Model
	sshUser     *textinput.Model
	sshKey      *textinput.Model
	sshPassword *textinput.Model
	knownHosts  *textinput.Model
	jumpDrafts  []jumpDraft
	jumpIdx     int
	jumpEditIdx int
	remoteDir   *textinput.Model

	jumpHostEdit       *textinput.Model
	jumpPortEdit       *textinput.Model
	jumpUserEdit       *textinput.Model
	jumpAuthEdit       string
	jumpKeyEdit        *textinput.Model
	jumpPasswordEdit   *textinput.Model
	jumpKnownHostsEdit *textinput.Model

	repoDrafts            []repoDraft
	repoIdx               int
	repoEditIdx           int
	repoFamilyEdit        string
	repoKeySourceEdit     string
	repoNameEdit          *textinput.Model
	repoURLEdit           *textinput.Model
	repoKeyValueEdit      *textinput.Model
	repoCAEdit            *textinput.Model
	repoAPTSuiteEdit      *textinput.Model
	repoAPTComponentsEdit *textinput.Model
	repoAPTFlatEdit       bool
	repoInsecureTLSEdit   bool

	pkgInput     *textinput.Model
	sudoPassword *textinput.Model

	settingsTemp     *textinput.Model
	settingsTimeout  *textinput.Model
	settingsLanguage *textinput.Model
	syslogHost       *textinput.Model
	syslogPort       *textinput.Model
	syslogProto      *textinput.Model
	syslogTag        *textinput.Model

	events   chan tea.Msg
	reporter *tuiReporter
}

func Run() error {
	model := newModel()
	defer model.close()
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func newModel() *Model {
	cwd, _ := os.Getwd()
	settingsPath, _ := config.DefaultPath()
	settings, settingsErr := config.Load(settingsPath)
	if settingsErr != nil {
		settings = config.Default()
	}
	i18n.SetFromSetting(settings.Language)
	m := &Model{
		screen:       screenWelcome,
		settingsPath: settingsPath,
		settings:     settings,
		events:       make(chan tea.Msg, 2000),
	}
	m.targetInput = input("ubuntu", "")
	m.versionInput = input("22.04", "")
	m.archInput = input("amd64", "")
	m.methodInput = input("docker", "")
	m.imageInput = input("", "")
	m.wslInput = input("", "")
	m.outInput = input(filepath.Join(cwd, "opf-output"), "")
	m.dbInput = input(filepath.Join(cwd, "opf.db"), "")

	m.sshHost = input("", "")
	m.sshPort = input("22", "")
	m.sshUser = input("", "")
	m.sshKey = input("", "")
	m.sshPassword = passwordInput("")
	m.knownHosts = input("", "")
	m.remoteDir = input("", "")

	m.jumpHostEdit = input("", "")
	m.jumpPortEdit = input("22", "")
	m.jumpUserEdit = input("", "")
	m.jumpAuthEdit = newJumpDraft().authMode
	m.jumpKeyEdit = input("", "")
	m.jumpPasswordEdit = passwordInput("")
	m.jumpKnownHostsEdit = input("", "")

	m.repoKeySourceEdit = repoKeySourceFile
	m.repoNameEdit = input("", "")
	m.repoURLEdit = input("", "")
	m.repoKeyValueEdit = input("", "")
	m.repoCAEdit = input("", "")
	m.repoAPTSuiteEdit = input("", "")
	m.repoAPTComponentsEdit = input("main", "")

	m.pkgInput = input("", "")
	m.sudoPassword = passwordInput("")
	m.settingsTemp = input(settings.TempDir, "")
	m.settingsTimeout = input(strconv.Itoa(settings.StageTimeoutSecs), "")
	m.settingsLanguage = input(languageSettingValue(settings.Language), "")
	m.syslogEnabled = settings.SyslogEnabled
	m.syslogHost = input(settings.SyslogHost, "")
	port := settings.SyslogPort
	if port == 0 {
		port = 514
	}
	m.syslogPort = input(strconv.Itoa(port), "")
	proto := settings.SyslogProto
	if proto == "" {
		proto = "udp"
	}
	m.syslogProto = input(proto, "")
	tag := settings.SyslogTag
	if tag == "" {
		tag = "opf"
	}
	m.syslogTag = input(tag, "")

	m.reporter = &tuiReporter{events: m.events}
	m.localizeInputs()
	m.openStore()
	m.resetSteps()
	return m
}

// localizeInputs refreshes the placeholder hints so they follow the active
// interface language, including after a language change in the settings screen.
func (m *Model) localizeInputs() {
	if m.targetInput != nil {
		m.targetInput.Placeholder = "ubuntu, debian, centos, alpine"
	}
	if m.repoURLEdit != nil {
		m.repoURLEdit.Placeholder = "https://repo.interno/caminho"
	}
	if m.repoAPTSuiteEdit != nil {
		m.repoAPTSuiteEdit.Placeholder = "bookworm, jammy"
	}
	if m.repoAPTComponentsEdit != nil {
		m.repoAPTComponentsEdit.Placeholder = "main"
	}
	if m.versionInput != nil {
		m.versionInput.Placeholder = i18n.T("versão")
	}
	if m.archInput != nil {
		m.archInput.Placeholder = i18n.T("amd64 ou x86_64")
	}
	if m.methodInput != nil {
		m.methodInput.Placeholder = i18n.T("docker, wsl ou auto")
	}
	if m.imageInput != nil {
		m.imageInput.Placeholder = i18n.T("opcional: opf/centos-7:f3")
	}
	if m.wslInput != nil {
		m.wslInput.Placeholder = i18n.T("opcional: ubuntu-24.04")
	}
	if m.outInput != nil {
		m.outInput.Placeholder = i18n.T("diretório de saída")
	}
	if m.dbInput != nil {
		m.dbInput.Placeholder = i18n.T("arquivo SQLite")
	}
	if m.sshHost != nil {
		m.sshHost.Placeholder = i18n.T("host do destino")
	}
	if m.sshPort != nil {
		m.sshPort.Placeholder = i18n.T("porta")
	}
	if m.sshUser != nil {
		m.sshUser.Placeholder = i18n.T("usuário")
	}
	if m.sshKey != nil {
		m.sshKey.Placeholder = i18n.T("caminho da chave (opcional)")
	}
	if m.sshPassword != nil {
		m.sshPassword.Placeholder = i18n.T("senha SSH/conta")
	}
	if m.knownHosts != nil {
		m.knownHosts.Placeholder = i18n.T("known_hosts (padrão do sistema)")
	}
	if m.remoteDir != nil {
		m.remoteDir.Placeholder = i18n.T("/tmp/opf-sessao (padrão)")
	}
	if m.jumpHostEdit != nil {
		m.jumpHostEdit.Placeholder = i18n.T("host da ponte")
	}
	if m.jumpPortEdit != nil {
		m.jumpPortEdit.Placeholder = i18n.T("porta")
	}
	if m.jumpUserEdit != nil {
		m.jumpUserEdit.Placeholder = i18n.T("usuário da ponte")
	}
	if m.jumpKeyEdit != nil {
		m.jumpKeyEdit.Placeholder = i18n.T("caminho da chave")
	}
	if m.jumpPasswordEdit != nil {
		m.jumpPasswordEdit.Placeholder = i18n.T("senha da ponte")
	}
	if m.jumpKnownHostsEdit != nil {
		m.jumpKnownHostsEdit.Placeholder = i18n.T("known_hosts (padrão do destino)")
	}
	if m.repoNameEdit != nil {
		m.repoNameEdit.Placeholder = i18n.T("nome do repositório")
	}
	if m.repoKeyValueEdit != nil {
		m.repoKeyValueEdit.Placeholder = i18n.T("chave do repositório")
	}
	if m.repoCAEdit != nil {
		m.repoCAEdit.Placeholder = i18n.T("CA customizada (opcional)")
	}
	if m.pkgInput != nil {
		m.pkgInput.Placeholder = i18n.T("ex.: vim, curl")
	}
	if m.sudoPassword != nil {
		m.sudoPassword.Placeholder = i18n.T("senha da conta para sudo")
	}
	if m.settingsTemp != nil {
		m.settingsTemp.Placeholder = i18n.T("diretório temporário")
	}
	if m.settingsTimeout != nil {
		m.settingsTimeout.Placeholder = i18n.T("segundos")
	}
	if m.settingsLanguage != nil {
		m.settingsLanguage.Placeholder = i18n.T("auto, en, pt-BR ou es")
	}
	if m.syslogHost != nil {
		m.syslogHost.Placeholder = i18n.T("host do Syslog")
	}
	if m.syslogPort != nil {
		m.syslogPort.Placeholder = i18n.T("porta")
	}
	if m.syslogProto != nil {
		m.syslogProto.Placeholder = i18n.T("udp ou tcp")
	}
	if m.syslogTag != nil {
		m.syslogTag.Placeholder = i18n.T("tag RFC 5424")
	}
}

// languageSettingValue normalizes an empty persisted setting to "auto".
func languageSettingValue(setting string) string {
	if strings.TrimSpace(setting) == "" {
		return i18n.SettingAuto
	}
	return setting
}

// normalizeLanguageSetting validates a language value typed in the TUI and
// returns the canonical persisted form. It accepts "auto", "en", "pt-BR" and
// "es" (case-insensitive), plus the common "pt"/"pt_BR" aliases.
func normalizeLanguageSetting(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return i18n.SettingAuto, true
	case "en":
		return string(i18n.EN), true
	case "pt-br", "pt", "pt_br":
		return string(i18n.PTBR), true
	case "es":
		return string(i18n.ES), true
	default:
		return "", false
	}
}

func input(value, placeholder string) *textinput.Model {
	in := textinput.New()
	in.SetValue(value)
	in.Placeholder = placeholder
	in.CharLimit = 512
	in.Width = 60
	in.Prompt = ""
	return &in
}

func passwordInput(placeholder string) *textinput.Model {
	in := input("", placeholder)
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	return in
}

func (m *Model) close() {
	if m.store != nil {
		_ = m.store.Close()
		m.store = nil
	}
}

func (m *Model) openStore() {
	if m.store != nil {
		_ = m.store.Close()
		m.store = nil
	}
	path := strings.TrimSpace(m.dbInput.Value())
	if path == "" {
		path = "opf.db"
	}
	st, err := store.Open(path)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.store = st
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.waitEvent())
}

func (m *Model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.events
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case pipelineStepMsg:
		if msg.step >= 1 && msg.step <= core.TotalEtapas {
			m.steps[msg.step-1] = msg.status
			m.stepName[msg.step-1] = msg.name
		}
		return m, m.waitEvent()
	case pipelineProgressMsg:
		m.progressText = fmt.Sprintf("%s: %d/%d", msg.stage, msg.current, msg.total)
		if msg.total > 0 {
			m.progress = float64(msg.current) / float64(msg.total)
		}
		return m, m.waitEvent()
	case pipelineLogMsg:
		m.appendLog("[" + msg.level + "] " + msg.message)
		return m, m.waitEvent()
	case confirmPromptMsg:
		m.confirm = &confirmRequest{count: msg.count, response: msg.response}
		return m, m.waitEvent()
	case pipelineDoneMsg:
		m.running = false
		m.cancel = nil
		m.result = msg.result
		m.runErr = msg.err
		if msg.result != nil {
			m.sessionID = msg.result.SessaoID
			m.resumeID = 0
		}
		m.openStore()
		m.screen = screenResult
		return m, nil
	case targetDetectMsg:
		if msg.err != nil {
			m.errorMsg = i18n.T("detecção: %s", msg.err.Error())
			return m, nil
		}
		m.targetInput.SetValue(msg.info.OS)
		m.versionInput.SetValue(msg.info.Version)
		m.archInput.SetValue(msg.info.Arch)
		m.imageInput.SetValue(msg.info.Image)
		m.manualTarget = false
		m.detectedHost = msg.host
		m.detectedUser = msg.user
		m.detectedPort = msg.port
		m.detectedJumps = msg.jumps
		m.detectedText = fmt.Sprintf("%s %s (%s) • %s", msg.info.OS, msg.info.Version, msg.info.Arch, msg.info.Family)
		if !msg.info.Tested {
			m.detectedText += " " + i18n.T("• preset não validado; revise a configuração avançada (ctrl+a)")
		}
		return m, nil
	case sshTestMsg:
		if msg.err != nil {
			m.errorMsg = i18n.T("SSH: %s", msg.err.Error())
		} else {
			m.errorMsg = i18n.T("SSH: conexão validada com host key.")
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}
	switch m.screen {
	case screenWelcome:
		return m.updateWelcome(msg)
	case screenProgress:
		return m.updateProgress(msg)
	case screenResult:
		return m.updateResult(msg)
	case screenHistory:
		return m.updateHistory(msg)
	case screenReport:
		switch key {
		case "esc", "q":
			m.screen = m.reportBack
			return m, nil
		case "up", "k":
			if m.reportOffset > 0 {
				m.reportOffset--
			}
		case "down", "j":
			m.reportOffset++
		case "pgup":
			m.reportOffset -= 15
			if m.reportOffset < 0 {
				m.reportOffset = 0
			}
		case "pgdown":
			m.reportOffset += 15
		case "J":
			m.exportReport("json")
		case "M":
			m.exportReport("md")
		case "S":
			m.sendReportSyslog()
		}
		return m, nil
	case screenSettings:
		return m.updateSettings(msg)
	case screenRepos:
		return m.updateRepos(msg)
	case screenRepoEdit:
		if key == "ctrl+s" {
			m.saveRepoEdit()
			return m, nil
		}
		return m.updateForm(msg)
	case screenJumps:
		return m.updateJumps(msg)
	case screenJumpEdit:
		if key == "ctrl+s" {
			m.saveJumpEdit()
			return m, nil
		}
		return m.updateForm(msg)
	default:
		return m.updateForm(msg)
	}
}

func (m *Model) updateWelcome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIdx > 0 {
			m.menuIdx--
		}
	case "down", "j":
		if m.menuIdx < 4 {
			m.menuIdx++
		}
	case "enter":
		switch m.menuIdx {
		case 0:
			m.result = nil
			m.resumeID = 0
			m.detectedText = ""
			m.detectedHost = ""
			m.detectedUser = ""
			m.detectedPort = ""
			m.detectedJumps = ""
			m.manualTarget = false
			m.jumpDrafts = nil
			m.jumpIdx = 0
			m.repoDrafts = nil
			m.repoIdx = 0
			m.screen = screenSSH
			m.buildForm()
		case 1:
			m.continueLatest()
		case 2:
			m.openHistory()
		case 3:
			m.screen = screenSettings
			m.buildForm()
		case 4:
			return m, tea.Quit
		}
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.screen == screenSSH && key == "ctrl+t" {
		return m, m.testSSH()
	}
	if m.screen == screenSSH && key == "ctrl+d" {
		return m, m.detectTarget()
	}
	if m.screen == screenConfig && key == "ctrl+r" {
		m.screen = screenRepos
		m.errorMsg = ""
		m.buildForm()
		return m, nil
	}
	if m.screen == screenSSH && key == "ctrl+j" {
		m.screen = screenJumps
		m.errorMsg = ""
		m.buildForm()
		return m, nil
	}
	if m.screen == screenSSH && key == "ctrl+a" {
		if m.sshEnabled && (m.detectedText == "" || !m.detectedMatchesSSH()) {
			m.targetInput.SetValue("")
			m.versionInput.SetValue("")
			m.archInput.SetValue("")
			m.imageInput.SetValue("")
		}
		m.screen = screenConfig
		m.buildForm()
		return m, nil
	}
	if key == "esc" {
		m.back()
		return m, nil
	}
	if key == "tab" || key == "down" {
		m.moveFocus(1)
		return m, nil
	}
	if key == "shift+tab" || key == "up" {
		m.moveFocus(-1)
		return m, nil
	}
	if key == "enter" {
		return m.advance()
	}
	if len(m.form) > 0 && m.focus < len(m.form) {
		item := &m.form[m.focus]
		if item.choice != nil {
			if key == " " && len(item.choices) > 0 {
				next := 0
				for i, choice := range item.choices {
					if choice == *item.choice {
						next = (i + 1) % len(item.choices)
						break
					}
				}
				*item.choice = item.choices[next]
			}
			return m, nil
		}
		if item.toggle != nil {
			if key == " " || key == "enter" {
				*item.toggle = !*item.toggle
			}
			if m.screen == screenSSH && key == "t" {
				return m, m.testSSH()
			}
			return m, nil
		}
		if item.input != nil {
			updated, cmd := item.input.Update(msg)
			*item.input = updated
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) moveFocus(delta int) {
	if len(m.form) == 0 {
		return
	}
	for _, item := range m.form {
		if item.input != nil {
			item.input.Blur()
		}
	}
	m.focus = (m.focus + delta + len(m.form)) % len(m.form)
	if m.form[m.focus].input != nil {
		_ = m.form[m.focus].input.Focus()
	}
}

func (m *Model) advance() (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenConfig:
		if strings.TrimSpace(m.targetInput.Value()) == "" || strings.TrimSpace(m.versionInput.Value()) == "" || strings.TrimSpace(m.archInput.Value()) == "" {
			m.errorMsg = i18n.T("distro, versão e arquitetura são obrigatórias")
			return m, nil
		}
		if _, err := m.buildRepositories(); err != nil {
			m.errorMsg = err.Error()
			return m, nil
		}
		m.manualTarget = true
		m.openStore()
		m.screen = screenPackages
		m.buildForm()
	case screenSSH:
		if m.sshEnabled {
			if _, err := m.buildSSHConfig(); err != nil {
				m.errorMsg = err.Error()
				return m, nil
			}
			if !m.manualTarget && (m.detectedText == "" || !m.detectedMatchesSSH()) {
				m.errorMsg = i18n.T("detectando o destino...")
				return m, m.detectTarget()
			}
			if strings.TrimSpace(m.versionInput.Value()) == "" || strings.TrimSpace(m.archInput.Value()) == "" {
				m.errorMsg = i18n.T("detecte o destino (ctrl+d) ou use a configuração avançada")
				return m, nil
			}
			m.screen = screenPackages
		} else {
			m.screen = screenConfig
		}
		m.buildForm()
	case screenPackages:
		return m.startRun()
	case screenJumpEdit:
		m.saveJumpEdit()
	case screenRepoEdit:
		m.saveRepoEdit()
	case screenSettings:
		m.saveSettings()
	}
	return m, nil
}

func (m *Model) back() {
	switch m.screen {
	case screenConfig, screenSSH:
		if m.screen == screenSSH {
			m.screen = screenConfig
		} else {
			m.screen = screenWelcome
		}
	case screenJumps:
		m.screen = screenSSH
	case screenJumpEdit:
		m.screen = screenJumps
	case screenRepos:
		m.screen = screenConfig
	case screenRepoEdit:
		m.screen = screenRepos
	case screenPackages:
		m.screen = screenSSH
	case screenSettings:
		m.screen = screenWelcome
	}
	m.buildForm()
}

func (m *Model) buildForm() {
	m.form = nil
	m.focus = 0
	switch m.screen {
	case screenConfig:
		m.form = []formItem{
			{label: i18n.T("Distro"), input: m.targetInput},
			{label: i18n.T("Versão"), input: m.versionInput},
			{label: i18n.T("Arquitetura"), input: m.archInput},
			{label: i18n.T("Fetch (docker/wsl/auto)"), input: m.methodInput},
			{label: i18n.T("Imagem Docker"), input: m.imageInput},
			{label: i18n.T("Distro WSL"), input: m.wslInput},
			{label: i18n.T("Saída"), input: m.outInput},
			{label: i18n.T("SQLite"), input: m.dbInput},
			{label: i18n.T("Incluir opcionais"), toggle: &m.optional},
		}
	case screenSSH:
		m.form = []formItem{
			{label: i18n.T("Habilitar SSH"), toggle: &m.sshEnabled},
			{label: i18n.T("Ignorar host key (inseguro)"), toggle: &m.skipHostKey},
			{label: i18n.T("Host"), input: m.sshHost},
			{label: i18n.T("Porta"), input: m.sshPort},
			{label: i18n.T("Usuário"), input: m.sshUser},
			{label: i18n.T("Chave privada"), input: m.sshKey},
			{label: i18n.T("Senha da conta"), input: m.sshPassword},
			{label: i18n.T("Known hosts"), input: m.knownHosts},
			{label: i18n.T("Diretório remoto"), input: m.remoteDir},
		}
	case screenJumpEdit:
		m.form = []formItem{
			{label: i18n.T("Host"), input: m.jumpHostEdit},
			{label: i18n.T("Porta"), input: m.jumpPortEdit},
			{label: i18n.T("Usuário"), input: m.jumpUserEdit},
			{label: i18n.T("Autenticação"), choice: &m.jumpAuthEdit, choices: []string{"password", "key", "key+password"}, choiceDisplay: jumpAuthDisplay},
			{label: i18n.T("Senha"), input: m.jumpPasswordEdit},
			{label: i18n.T("Chave privada"), input: m.jumpKeyEdit},
			{label: i18n.T("Known hosts"), input: m.jumpKnownHostsEdit},
		}
	case screenRepoEdit:
		m.form = []formItem{
			{label: i18n.T("Nome"), input: m.repoNameEdit},
			{label: i18n.T("URL"), input: m.repoURLEdit},
			{label: i18n.T("Chave"), choice: &m.repoKeySourceEdit, choices: []string{repoKeySourceFile, repoKeySourceURL, repoKeySourceNone}, choiceDisplay: repoKeySourceDisplay},
			{label: i18n.T("Chave (arquivo/URL)"), input: m.repoKeyValueEdit},
			{label: i18n.T("CA customizada"), input: m.repoCAEdit},
		}
		if m.repoFamilyEdit == core.RepositoryFamilyAPT {
			m.form = append(m.form,
				formItem{label: i18n.T("Suite APT"), input: m.repoAPTSuiteEdit},
				formItem{label: i18n.T("Components APT"), input: m.repoAPTComponentsEdit},
				formItem{label: i18n.T("Flat"), toggle: &m.repoAPTFlatEdit},
			)
		}
		if m.repoFamilyEdit != core.RepositoryFamilyAPK {
			m.form = append(m.form, formItem{label: i18n.T("TLS inseguro"), toggle: &m.repoInsecureTLSEdit})
		}
	case screenPackages:
		m.form = []formItem{
			{label: i18n.T("Pacotes (vírgula)"), input: m.pkgInput},
			{label: i18n.T("Opcionais"), toggle: &m.optional},
			{label: i18n.T("Forçar reinstalação"), toggle: &m.force},
			{label: i18n.T("Instalar"), toggle: &m.install},
			{label: i18n.T("Usar sudo"), toggle: &m.sudo},
			{label: i18n.T("Senha do sudo"), input: m.sudoPassword},
		}
	case screenSettings:
		m.form = []formItem{
			{label: i18n.T("Idioma da interface"), input: m.settingsLanguage},
			{label: i18n.T("Diretório temporário"), input: m.settingsTemp},
			{label: i18n.T("Timeout (segundos)"), input: m.settingsTimeout},
			{label: i18n.T("Habilitar Syslog"), toggle: &m.syslogEnabled},
			{label: i18n.T("Host Syslog"), input: m.syslogHost},
			{label: i18n.T("Porta Syslog"), input: m.syslogPort},
			{label: i18n.T("Protocolo Syslog"), input: m.syslogProto},
			{label: i18n.T("Tag Syslog"), input: m.syslogTag},
		}
	}
	if len(m.form) > 0 && m.form[0].input != nil {
		_ = m.form[0].input.Focus()
	}
}

func (m *Model) resetSteps() {
	for i := 0; i < core.TotalEtapas; i++ {
		m.steps[i] = "[ ]"
		m.stepName[i] = core.NomeEtapa(i + 1)
	}
}

func (m *Model) appendLog(line string) {
	m.logs = append(m.logs, strings.TrimRight(line, "\r\n"))
	if len(m.logs) > 400 {
		m.logs = m.logs[len(m.logs)-400:]
	}
}
