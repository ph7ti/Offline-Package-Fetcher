package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"opf/internal/app"
	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/observability"
	"opf/internal/transfer"
)

func (m *Model) startRun() (tea.Model, tea.Cmd) {
	cfg, err := m.buildConfig()
	if err != nil {
		m.errorMsg = err.Error()
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.errorMsg = ""
	m.result = nil
	m.runErr = nil
	m.confirm = nil
	m.progress = 0
	m.progressText = "Iniciando"
	m.logs = nil
	m.resetSteps()
	m.screen = screenProgress
	resumeID := m.resumeID
	runCmd := func() tea.Msg {
		if cfg.Install && cfg.SSH.Host != "" {
			isRoot, probeErr := app.DetectRoot(ctx, cfg.SSH)
			if probeErr != nil {
				return pipelineDoneMsg{err: fmt.Errorf("detectar privilégios remotos: %w", probeErr)}
			}
			if isRoot {
				cfg.UseSudo = false
				cfg.SudoPassword = ""
			} else if !cfg.UseSudo {
				return pipelineDoneMsg{err: errors.New("usuário remoto não é root; habilite “Usar sudo” e informe a senha da conta")}
			}
		}
		svc, createErr := app.New(cfg)
		if createErr != nil {
			return pipelineDoneMsg{err: createErr}
		}
		defer svc.Close()
		var result *core.PipelineResult
		if resumeID > 0 {
			result, createErr = svc.Resume(ctx, resumeID)
		} else {
			result, createErr = svc.Run(ctx)
		}
		return pipelineDoneMsg{result: result, err: createErr}
	}
	return m, runCmd
}

type targetDetectMsg struct {
	info  *app.TargetInfo
	err   error
	host  string
	user  string
	port  string
	jumps string
}

func (m *Model) detectedMatchesSSH() bool {
	return m.detectedHost == m.sshHost.Value() && m.detectedUser == m.sshUser.Value() &&
		m.detectedPort == m.sshPort.Value() && m.detectedJumps == m.jumpFingerprint()
}

func (m *Model) detectTarget() tea.Cmd {
	cfg, err := m.buildSSHConfig()
	if err != nil {
		return func() tea.Msg { return targetDetectMsg{err: err} }
	}
	host := m.sshHost.Value()
	user := m.sshUser.Value()
	port := m.sshPort.Value()
	jumps := m.jumpFingerprint()
	return func() tea.Msg {
		info, detectErr := app.DetectRemoteTarget(context.Background(), cfg)
		return targetDetectMsg{info: info, err: detectErr, host: host, user: user, port: port, jumps: jumps}
	}
}

func (m *Model) buildConfig() (app.Config, error) {
	packages := splitList(m.pkgInput.Value())
	if len(packages) == 0 {
		return app.Config{}, errors.New("informe ao menos um pacote")
	}
	repositories, err := m.buildRepositories()
	if err != nil {
		return app.Config{}, err
	}
	var sshCfg core.SSHConfig
	if m.sshEnabled {
		var err error
		sshCfg, err = m.buildSSHConfig()
		if err != nil {
			return app.Config{}, err
		}
	}
	install := m.sshEnabled && m.install
	sudoPassword := ""
	if m.sudo {
		sudoPassword = m.sudoPassword.Value()
		if sudoPassword == "" {
			sudoPassword = m.sshPassword.Value()
		}
	}
	if install && m.sudo && sudoPassword == "" {
		return app.Config{}, errors.New("sudo exige a senha da conta")
	}
	cfg := app.Config{
		StorePath:       strings.TrimSpace(m.dbInput.Value()),
		OutputBaseDir:   strings.TrimSpace(m.outInput.Value()),
		TempDir:         strings.TrimSpace(m.settings.TempDir),
		TargetOS:        strings.ToLower(strings.TrimSpace(m.targetInput.Value())),
		TargetVersion:   strings.TrimSpace(m.versionInput.Value()),
		TargetArch:      strings.TrimSpace(m.archInput.Value()),
		Packages:        packages,
		IncludeOptional: m.optional,
		FetchMethod:     strings.ToLower(strings.TrimSpace(m.methodInput.Value())),
		Image:           strings.TrimSpace(m.imageInput.Value()),
		WSLDistro:       strings.TrimSpace(m.wslInput.Value()),
		SSH:             sshCfg,
		RemoteDir:       strings.TrimSpace(m.remoteDir.Value()),
		Repositories:    repositories,
		ForceReinstall:  m.force,
		StageTimeout:    m.settings.StageTimeout(),
		Reporter:        m.reporter,
		Install:         install,
		UseSudo:         install && m.sudo,
		SudoPassword:    sudoPassword,
		SyslogEnabled:   m.settings.SyslogEnabled,
		SyslogHost:      m.settings.SyslogHost,
		SyslogPort:      m.settings.SyslogPort,
		SyslogProto:     m.settings.SyslogProto,
		SyslogTag:       m.settings.SyslogTag,
	}
	if install {
		cfg.ConfirmInstall = m.confirmInstall
	}
	if !m.sshEnabled {
		cfg.SSH = core.SSHConfig{}
	}
	return cfg, nil
}

func (m *Model) confirmInstall(ctx context.Context, artifacts []core.PackageArtifact) (bool, error) {
	response := make(chan bool, 1)
	select {
	case m.events <- confirmPromptMsg{count: len(artifacts), response: response}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case ok := <-response:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

type confirmPromptMsg struct {
	count    int
	response chan bool
}

func (m *Model) updateProgress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		switch msg.String() {
		case "y", "s":
			select {
			case m.confirm.response <- true:
			default:
			}
			m.confirm = nil
		case "n", "esc":
			select {
			case m.confirm.response <- false:
			default:
			}
			m.confirm = nil
		}
		return m, nil
	}
	switch msg.String() {
	case "c":
		if m.cancel != nil {
			m.reporter.Log("warn", "cancelamento solicitado")
			m.cancel()
		}
	}
	return m, nil
}

func (m *Model) updateResult(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		id := m.sessionID
		if m.result != nil {
			id = m.result.SessaoID
		}
		m.showReport(id, screenResult)
	case "h":
		m.openHistory()
	case "n":
		m.result = nil
		m.runErr = nil
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
	case "esc", "q":
		m.screen = screenWelcome
	}
	return m, nil
}

func (m *Model) updateHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenWelcome
	case "up", "k":
		if m.sessionIdx > 0 {
			m.sessionIdx--
		}
	case "down", "j":
		if m.sessionIdx < len(m.sessions)-1 {
			m.sessionIdx++
		}
	case "enter":
		if m.sessionIdx >= 0 && m.sessionIdx < len(m.sessions) {
			m.showReport(m.sessions[m.sessionIdx].ID, screenHistory)
		}
	case "r":
		if m.sessionIdx >= 0 && m.sessionIdx < len(m.sessions) {
			m.resumeSession(m.sessions[m.sessionIdx].ID)
		}
	case "a":
		if m.sessionIdx >= 0 && m.sessionIdx < len(m.sessions) && m.store != nil {
			_ = m.store.ArquivarSessao(context.Background(), m.sessions[m.sessionIdx].ID, true)
			m.openHistory()
		}
	}
	return m, nil
}

func (m *Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+s" {
		m.saveSettings()
		return m, nil
	}
	return m.updateForm(msg)
}

func (m *Model) saveSettings() {
	timeout, err := strconv.Atoi(strings.TrimSpace(m.settingsTimeout.Value()))
	if err != nil || timeout <= 0 {
		m.errorMsg = "timeout inválido"
		return
	}
	port, portErr := strconv.Atoi(strings.TrimSpace(m.syslogPort.Value()))
	if portErr != nil || port <= 0 || port > 65535 {
		m.errorMsg = "porta Syslog inválida"
		return
	}
	m.settings.TempDir = strings.TrimSpace(m.settingsTemp.Value())
	m.settings.StageTimeoutSecs = timeout
	m.settings.SyslogEnabled = m.syslogEnabled
	m.settings.SyslogHost = strings.TrimSpace(m.syslogHost.Value())
	m.settings.SyslogPort = port
	m.settings.SyslogProto = strings.ToLower(strings.TrimSpace(m.syslogProto.Value()))
	m.settings.SyslogTag = strings.TrimSpace(m.syslogTag.Value())
	if m.settings.SyslogEnabled && m.settings.SyslogHost == "" {
		m.errorMsg = "host Syslog é obrigatório quando habilitado"
		return
	}
	if err := config.Save(m.settingsPath, m.settings); err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.errorMsg = "configurações salvas"
}

func (m *Model) openHistory() {
	if m.store == nil {
		m.openStore()
	}
	if m.store == nil {
		m.errorMsg = "SQLite indisponível"
		return
	}
	sessoes, err := m.store.ListarSessoes(context.Background(), true)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.sessions = sessoes
	m.sessionIdx = 0
	m.screen = screenHistory
}

func (m *Model) continueLatest() {
	m.openHistory()
	for i, sessao := range m.sessions {
		if sessao.Status != core.SessaoConcluida {
			m.sessionIdx = i
			m.resumeSession(sessao.ID)
			return
		}
	}
	m.errorMsg = "nenhuma sessão inacabada"
}

type sessionConfigJSON struct {
	TargetOS      string                   `json:"target_os"`
	TargetVersion string                   `json:"target_version"`
	TargetArch    string                   `json:"target_arch"`
	Packages      []string                 `json:"packages"`
	FetchMethod   string                   `json:"fetch_method"`
	Image         string                   `json:"image"`
	WSLDistro     string                   `json:"wsl_distro"`
	RemoteDir     string                   `json:"remote_dir"`
	SSHHost       string                   `json:"ssh_host"`
	SSHPort       int                      `json:"ssh_port"`
	SSHUser       string                   `json:"ssh_user"`
	SSHJumps      []app.SSHJumpMetadata    `json:"ssh_jumps"`
	Repositories  []core.PackageRepository `json:"repositories"`
	Install       bool                     `json:"install"`
}

func (m *Model) resumeSession(id int64) {
	if m.store == nil {
		m.openStore()
	}
	if m.store == nil {
		m.errorMsg = "SQLite indisponível"
		return
	}
	sessao, err := m.store.ObterSessao(context.Background(), id)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	var cfg sessionConfigJSON
	if sessao.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(sessao.ConfigJSON), &cfg)
	}
	if cfg.TargetOS != "" {
		m.targetInput.SetValue(cfg.TargetOS)
	}
	if cfg.TargetVersion != "" {
		m.versionInput.SetValue(cfg.TargetVersion)
	}
	if cfg.TargetArch != "" {
		m.archInput.SetValue(cfg.TargetArch)
	}
	m.setRepositories(cfg.Repositories)
	if cfg.FetchMethod != "" {
		m.methodInput.SetValue(cfg.FetchMethod)
	}
	m.imageInput.SetValue(cfg.Image)
	m.wslInput.SetValue(cfg.WSLDistro)
	m.remoteDir.SetValue(cfg.RemoteDir)
	if cfg.SSHHost != "" {
		m.sshEnabled = true
		m.sshHost.SetValue(cfg.SSHHost)
		m.sshUser.SetValue(cfg.SSHUser)
		if cfg.SSHPort > 0 {
			m.sshPort.SetValue(strconv.Itoa(cfg.SSHPort))
		}
	} else if sessao.HostDestino != "" {
		m.sshEnabled = true
		m.sshHost.SetValue(sessao.HostDestino)
	}
	needsJumpPassword := m.setJumpMetadata(cfg.SSHJumps)
	m.install = cfg.Install
	if len(cfg.Packages) > 0 {
		m.pkgInput.SetValue(strings.Join(cfg.Packages, ", "))
	} else if solicitados, err := m.store.ListarPacotesSolicitados(context.Background(), id); err == nil {
		var names []string
		for _, p := range solicitados {
			names = append(names, p.Nome)
		}
		m.pkgInput.SetValue(strings.Join(names, ", "))
	}
	m.resumeID = id
	m.detectedText = ""
	m.detectedHost = ""
	m.detectedUser = ""
	m.detectedPort = ""
	m.detectedJumps = ""
	m.manualTarget = true
	m.screen = screenSSH
	m.buildForm()
	message := fmt.Sprintf("Retomando sessão #%d; reautentique e revise os dados.", id)
	if needsJumpPassword {
		message += " As senhas das pontes não são restauradas; informe-as novamente."
	}
	m.errorMsg = message
}

func (m *Model) showReport(sessionID int64, back screen) {
	if sessionID == 0 {
		sessionID = m.sessionID
	}
	m.reportSessionID = sessionID
	m.reportBack = back
	m.errorMsg = ""
	m.reportText = m.buildReport(sessionID)
	m.reportOffset = 0
	m.screen = screenReport
}

func (m *Model) exportReport(format string) {
	if m.store == nil {
		m.openStore()
	}
	if m.store == nil {
		m.errorMsg = "SQLite indisponível"
		return
	}
	id := m.reportSessionID
	if id == 0 {
		id = m.sessionID
	}
	relatorio, err := observability.GerarRelatorio(context.Background(), m.store, id)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	dir := strings.TrimSpace(m.outInput.Value())
	if dir == "" {
		dir, _ = os.Getwd()
	}
	base := filepath.Join(dir, fmt.Sprintf("opf-relatorio-%d", id))
	switch format {
	case "json":
		base += ".json"
		err = observability.SalvarJSON(base, relatorio)
	case "md":
		base += ".md"
		err = observability.SalvarMarkdown(base, relatorio)
	default:
		err = errors.New("formato de relatório inválido")
	}
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.errorMsg = "Relatório gerado: " + base
}

func (m *Model) sendReportSyslog() {
	if !m.settings.SyslogEnabled {
		m.errorMsg = "habilite o Syslog nas configurações globais"
		return
	}
	if m.store == nil {
		m.openStore()
	}
	if m.store == nil {
		m.errorMsg = "SQLite indisponível"
		return
	}
	id := m.reportSessionID
	if id == 0 {
		id = m.sessionID
	}
	relatorio, err := observability.GerarRelatorio(context.Background(), m.store, id)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	err = observability.SendRelatorioSyslog(context.Background(), observability.SyslogConfig{
		Enabled: true, Host: m.settings.SyslogHost, Port: m.settings.SyslogPort,
		Proto: m.settings.SyslogProto, Tag: m.settings.SyslogTag,
	}, relatorio)
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.errorMsg = "Relatório enfileirado ao Syslog (best-effort)."
}

func (m *Model) buildReport(sessionID int64) string {
	if m.store == nil {
		m.openStore()
	}
	if m.store == nil {
		return "SQLite indisponível."
	}
	relatorio, err := observability.GerarRelatorio(context.Background(), m.store, sessionID)
	if err != nil {
		return err.Error()
	}
	return relatorio.Markdown()
}

func (m *Model) testSSH() tea.Cmd {
	return func() tea.Msg {
		cfg, err := m.buildSSHConfig()
		if err != nil {
			return sshTestMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		manager := transfer.NewSSHTransferManager()
		if err := manager.Connect(ctx, cfg); err != nil {
			return sshTestMsg{err: err}
		}
		_ = manager.Close()
		return sshTestMsg{}
	}
}

func (m *Model) buildSSHConfig() (core.SSHConfig, error) {
	port := 22
	if strings.TrimSpace(m.sshPort.Value()) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(m.sshPort.Value()))
		if err != nil || parsed <= 0 || parsed > 65535 {
			return core.SSHConfig{}, errors.New("porta SSH inválida")
		}
		port = parsed
	}
	cfg := core.SSHConfig{Host: strings.TrimSpace(m.sshHost.Value()), Port: port, User: strings.TrimSpace(m.sshUser.Value()), KeyPath: strings.TrimSpace(m.sshKey.Value()), Password: m.sshPassword.Value(), KnownHosts: strings.TrimSpace(m.knownHosts.Value()), InsecureSkipHostKey: m.skipHostKey}
	if cfg.Host == "" || cfg.User == "" {
		return cfg, errors.New("host e usuário SSH são obrigatórios")
	}
	jumps, err := m.buildJumpHosts(cfg)
	if err != nil {
		return cfg, err
	}
	cfg.JumpHosts = jumps
	return cfg, nil
}

func splitList(value string) []string {
	value = strings.ReplaceAll(value, ",", "\n")
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}
