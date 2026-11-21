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

	settingsTemp    *textinput.Model
	settingsTimeout *textinput.Model
	syslogHost      *textinput.Model
	syslogPort      *textinput.Model
	syslogProto     *textinput.Model
	syslogTag       *textinput.Model

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
	settings, _ := config.Load(settingsPath)
	m := &Model{
		screen:       screenWelcome,
		settingsPath: settingsPath,
		settings:     settings,
		events:       make(chan tea.Msg, 2000),
	}
	m.targetInput = input("ubuntu", "ubuntu, debian, centos, alpine")
	m.versionInput = input("22.04", "versão")
	m.archInput = input("amd64", "amd64 ou x86_64")
	m.methodInput = input("docker", "docker, wsl ou auto")
	m.imageInput = input("", "opcional: opf/centos-7:f3")
	m.wslInput = input("", "opcional: ubuntu-24.04")
	m.outInput = input(filepath.Join(cwd, "opf-output"), "diretório de saída")
	m.dbInput = input(filepath.Join(cwd, "opf.db"), "arquivo SQLite")

	m.sshHost = input("", "host do destino")
	m.sshPort = input("22", "porta")
	m.sshUser = input("", "usuário")
	m.sshKey = input("", "caminho da chave (opcional)")
	m.sshPassword = passwordInput("senha SSH/conta")
	m.knownHosts = input("", "known_hosts (padrão do sistema)")
	m.remoteDir = input("", "/tmp/opf-sessao (padrão)")

	m.jumpHostEdit = input("", "host da ponte")
	m.jumpPortEdit = input("22", "porta")
	m.jumpUserEdit = input("", "usuário da ponte")
	m.jumpAuthEdit = newJumpDraft().authMode
	m.jumpKeyEdit = input("", "caminho da chave")
	m.jumpPasswordEdit = passwordInput("senha da ponte")
	m.jumpKnownHostsEdit = input("", "known_hosts (padrão do destino)")

	m.repoKeySourceEdit = repoKeySourceFile
	m.repoNameEdit = input("", "nome do repositório")
	m.repoURLEdit = input("", "https://repo.interno/caminho")
	m.repoKeyValueEdit = input("", "chave do repositório")
	m.repoCAEdit = input("", "CA customizada (opcional)")
	m.repoAPTSuiteEdit = input("", "bookworm, jammy")
	m.repoAPTComponentsEdit = input("main", "main")

	m.pkgInput = input("", "ex.: vim, curl")
	m.sudoPassword = passwordInput("senha da conta para sudo")
	m.settingsTemp = input(settings.TempDir, "diretório temporário")
	m.settingsTimeout = input(strconv.Itoa(settings.StageTimeoutSecs), "segundos")
	m.syslogEnabled = settings.SyslogEnabled
	m.syslogHost = input(settings.SyslogHost, "host do Syslog")
	port := settings.SyslogPort
	if port == 0 {
		port = 514
	}
	m.syslogPort = input(strconv.Itoa(port), "porta")
	proto := settings.SyslogProto
	if proto == "" {
		proto = "udp"
	}
	m.syslogProto = input(proto, "udp ou tcp")
	tag := settings.SyslogTag
	if tag == "" {
		tag = "opf"
	}
	m.syslogTag = input(tag, "tag RFC 5424")

	m.reporter = &tuiReporter{events: m.events}
	m.openStore()
	m.resetSteps()
	return m
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
			m.errorMsg = "detecção: " + msg.err.Error()
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
			m.detectedText += " • preset não validado; revise a configuração avançada (ctrl+a)"
		}
		return m, nil
	case sshTestMsg:
		if msg.err != nil {
			m.errorMsg = "SSH: " + msg.err.Error()
		} else {
			m.errorMsg = "SSH: conexão validada com host key."
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
			m.errorMsg = "distro, versão e arquitetura são obrigatórias"
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
				m.errorMsg = "detectando o destino..."
				return m, m.detectTarget()
			}
			if strings.TrimSpace(m.versionInput.Value()) == "" || strings.TrimSpace(m.archInput.Value()) == "" {
				m.errorMsg = "detecte o destino (ctrl+d) ou use a configuração avançada"
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
			{label: "Distro", input: m.targetInput},
			{label: "Versão", input: m.versionInput},
			{label: "Arquitetura", input: m.archInput},
			{label: "Fetch (docker/wsl/auto)", input: m.methodInput},
			{label: "Imagem Docker", input: m.imageInput},
			{label: "Distro WSL", input: m.wslInput},
			{label: "Saída", input: m.outInput},
			{label: "SQLite", input: m.dbInput},
			{label: "Incluir opcionais", toggle: &m.optional},
		}
	case screenSSH:
		m.form = []formItem{
			{label: "Habilitar SSH", toggle: &m.sshEnabled},
			{label: "Ignorar host key (inseguro)", toggle: &m.skipHostKey},
			{label: "Host", input: m.sshHost},
			{label: "Porta", input: m.sshPort},
			{label: "Usuário", input: m.sshUser},
			{label: "Chave privada", input: m.sshKey},
			{label: "Senha da conta", input: m.sshPassword},
			{label: "Known hosts", input: m.knownHosts},
			{label: "Diretório remoto", input: m.remoteDir},
		}
	case screenJumpEdit:
		m.form = []formItem{
			{label: "Host", input: m.jumpHostEdit},
			{label: "Porta", input: m.jumpPortEdit},
			{label: "Usuário", input: m.jumpUserEdit},
			{label: "Autenticação", choice: &m.jumpAuthEdit, choices: []string{"password", "key", "key+password"}, choiceDisplay: jumpAuthDisplay},
			{label: "Senha", input: m.jumpPasswordEdit},
			{label: "Chave privada", input: m.jumpKeyEdit},
			{label: "Known hosts", input: m.jumpKnownHostsEdit},
		}
	case screenRepoEdit:
		m.form = []formItem{
			{label: "Nome", input: m.repoNameEdit},
			{label: "URL", input: m.repoURLEdit},
			{label: "Chave", choice: &m.repoKeySourceEdit, choices: []string{repoKeySourceFile, repoKeySourceURL, repoKeySourceNone}, choiceDisplay: repoKeySourceDisplay},
			{label: "Chave (arquivo/URL)", input: m.repoKeyValueEdit},
			{label: "CA customizada", input: m.repoCAEdit},
		}
		if m.repoFamilyEdit == core.RepositoryFamilyAPT {
			m.form = append(m.form,
				formItem{label: "Suite APT", input: m.repoAPTSuiteEdit},
				formItem{label: "Components APT", input: m.repoAPTComponentsEdit},
				formItem{label: "Flat", toggle: &m.repoAPTFlatEdit},
			)
		}
		if m.repoFamilyEdit != core.RepositoryFamilyAPK {
			m.form = append(m.form, formItem{label: "TLS inseguro", toggle: &m.repoInsecureTLSEdit})
		}
	case screenPackages:
		m.form = []formItem{
			{label: "Pacotes (vírgula)", input: m.pkgInput},
			{label: "Opcionais", toggle: &m.optional},
			{label: "Forçar reinstalação", toggle: &m.force},
			{label: "Instalar", toggle: &m.install},
			{label: "Usar sudo", toggle: &m.sudo},
			{label: "Senha do sudo", input: m.sudoPassword},
		}
	case screenSettings:
		m.form = []formItem{
			{label: "Diretório temporário", input: m.settingsTemp},
			{label: "Timeout (segundos)", input: m.settingsTimeout},
			{label: "Habilitar Syslog", toggle: &m.syslogEnabled},
			{label: "Host Syslog", input: m.syslogHost},
			{label: "Porta Syslog", input: m.syslogPort},
			{label: "Protocolo Syslog", input: m.syslogProto},
			{label: "Tag Syslog", input: m.syslogTag},
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
