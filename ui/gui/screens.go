//go:build cgo

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	opfapp "opf/internal/app"
	"opf/internal/config"
	"opf/internal/core"
	"opf/internal/observability"
)

func (u *UI) showWelcome() {
	if u.store == nil {
		u.openStore()
	}
	sessoes, sessionErr := u.allSessions()
	totalPackages, totalInstalled := 0, 0
	for _, sessao := range sessoes {
		totalPackages += sessao.TotalPacotes
		totalInstalled += sessao.TotalInstalados
	}

	dbName := filepath.Base(strings.TrimSpace(u.dbEntry.Text))
	if dbName == "." || dbName == "" {
		dbName = "opf.db"
	}
	quickActions := u.sectionCard("Ações rápidas", "Inicie uma operação ou consulte o histórico local.", container.NewHBox(
		u.primaryButton("Nova sessão", theme.ContentAddIcon(), u.startNewSession),
		u.secondaryButton("Continuar", theme.MediaPlayIcon(), u.continueLatest),
		u.ghostButton("Histórico", theme.HistoryIcon(), u.showHistory),
		u.ghostButton("Configurações", theme.SettingsIcon(), u.showSettings),
		u.ghostButton("Atualizar", theme.ViewRefreshIcon(), u.showWelcome),
	))
	stats := container.NewGridWithColumns(2,
		u.statTile("Sessões", strconv.Itoa(len(sessoes)), "registradas localmente", theme.HistoryIcon()),
		u.statTile("Pacotes", strconv.Itoa(totalPackages), "artefatos rastreados", theme.DownloadIcon()),
		u.statTile("Instalados", strconv.Itoa(totalInstalled), "confirmados no destino", theme.ConfirmIcon()),
		u.statTile("Banco de dados", "SQLite", dbName, theme.StorageIcon()),
	)

	recentObjects := make([]fyne.CanvasObject, 0, 6)
	if sessionErr != nil {
		recentObjects = append(recentObjects, u.banner(sessionErr.Error(), toneError))
	} else if len(sessoes) == 0 {
		recentObjects = append(recentObjects, u.banner("Nenhuma sessão registrada ainda. Comece com uma nova sessão.", toneInfo))
	} else {
		limit := len(sessoes)
		if limit > 4 {
			limit = 4
		}
		for _, sessao := range sessoes[:limit] {
			recentObjects = append(recentObjects, u.sessionCard(sessao, u.showWelcome), vSpace(8))
		}
	}
	recentObjects = append(recentObjects, u.ghostButton("Ver histórico completo", theme.HistoryIcon(), u.showHistory))
	recent := u.sectionCard("Sessões recentes", "Abra o relatório, retome uma operação interrompida ou audite os últimos resultados.", container.NewVBox(recentObjects...))

	u.showScreen("Início", container.NewVBox(quickActions, vSpace(12), stats, vSpace(12), recent))
}
func (u *UI) allSessions() ([]core.SessaoResumo, error) {
	if u.store == nil {
		return nil, fmt.Errorf("SQLite indisponível")
	}
	return u.store.ListarSessoes(context.Background(), true)
}

func (u *UI) showConfig() {
	targetForm := widget.NewForm(
		widget.NewFormItem("Distro de destino", u.targetSelect),
		widget.NewFormItem("Versão", u.versionEntry),
		widget.NewFormItem("Arquitetura", u.archEntry),
	)
	environmentForm := widget.NewForm(
		widget.NewFormItem("Método de fetch", u.methodSelect),
		widget.NewFormItem("Imagem Docker (opcional)", u.imageEntry),
		widget.NewFormItem("Distro WSL (opcional)", u.wslEntry),
	)
	storageForm := widget.NewForm(
		widget.NewFormItem("Diretório de saída", u.outEntry),
	)
	targetCard := u.sectionCard("Sistema de destino", "O pipeline baixa artefatos compatíveis com este alvo.", targetForm)
	environmentCard := u.sectionCard("Ambiente de download", "Docker é o caminho validado; WSL permanece disponível para famílias APT.", environmentForm)
	u.repoList = container.NewVBox()
	addRepo := u.secondaryButton("Adicionar repositório", theme.ContentAddIcon(), u.addRepoRow)
	repoCard := u.sectionCard("Repositórios adicionais", "Usados apenas no ambiente isolado de download; a lista é aditiva e não substitui os repositórios oficiais.", container.NewVBox(
		u.muted(fmt.Sprintf("Família atual: %s. Nenhuma credencial de repositório é armazenada.", u.repositoryFamilyOrApt()), theme.SizeNameCaptionText),
		vSpace(8),
		u.repoList,
		vSpace(8),
		container.NewHBox(addRepo, layout.NewSpacer()),
	))
	u.refreshRepoList()
	optionsCard := u.sectionCard("Saída e opções", "Os artefatos e o manifesto SHA256SUMS ficam no diretório de saída. O banco é definido em Configurações Globais.", container.NewVBox(
		storageForm,
		vSpace(8),
		u.optionalCheck,
	))
	note := u.banner("Presets validados: Ubuntu 22.04/24.04, Debian 12, CentOS 7, Alpine 3.20 e famílias RPM recentes. “Automático” usa WSL compatível no Windows e Docker nos demais casos.", toneInfo)
	content := container.NewVBox(
		targetCard, vSpace(14),
		environmentCard, vSpace(14),
		repoCard, vSpace(14),
		optionsCard, vSpace(12),
		note, vSpace(8),
		u.nav(u.showSSH, "Continuar", func() {
			if u.targetSelect.Selected == "" || strings.TrimSpace(u.versionEntry.Text) == "" || strings.TrimSpace(u.archEntry.Text) == "" {
				u.showError(fmt.Errorf("distro, versão e arquitetura são obrigatórias"))
				return
			}
			u.manualTarget = true
			u.openStore()
			u.showPackages()
		}),
	)
	u.showScreen("Configuração da Sessão", content)
}

func (u *UI) showSSH() {
	if u.sshPort.Text == "" {
		u.sshPort.SetText("22")
	}
	u.testStatus = widget.NewLabel("Teste de conexão: não executado")
	u.testStatus.SizeName = theme.SizeNameCaptionText
	u.testStatus.Wrapping = fyne.TextWrapWord
	u.detectStatus.SizeName = theme.SizeNameCaptionText
	u.detectStatus.Wrapping = fyne.TextWrapWord
	if u.detected != nil && !u.detectedMatchesSSH() {
		u.detectStatus.SetText("Configuração SSH alterada; detecte novamente.")
	}

	connectionCard := u.sectionCard("Conexão", "Use chave ou senha. A host key é verificada estritamente por padrão.", widget.NewForm(
		widget.NewFormItem("Host do destino", u.sshHost),
		widget.NewFormItem("Porta", u.sshPort),
		widget.NewFormItem("Usuário", u.sshUser),
		widget.NewFormItem("Chave privada", u.sshKey),
		widget.NewFormItem("Senha da conta", u.sshPassword),
		widget.NewFormItem("Known hosts", u.knownHosts),
	))
	u.jumpList = container.NewVBox()
	addJump := u.secondaryButton("Adicionar ponte", theme.ContentAddIcon(), u.addJumpRow)
	jumpCard := u.sectionCard("Pontes SSH e destino remoto", "As pontes são abertas em ordem; cada uma atravessa a anterior e usa credenciais próprias.", container.NewVBox(
		u.muted("Senhas das pontes ficam somente em memória e não são restauradas ao retomar; sessões antigas podem exigir reconfigurar as pontes.", theme.SizeNameCaptionText),
		vSpace(8),
		u.jumpList,
		vSpace(8),
		container.NewHBox(addJump, layout.NewSpacer()),
		vSpace(8),
		widget.NewForm(
			widget.NewFormItem("Diretório remoto", u.remoteDir),
		),
	))
	u.refreshJumpList()
	skipWarning := u.banner("Host key ignorada: a conexão fica vulnerável a MITM. Use apenas em ambiente controlado.", toneError)
	if !u.skipHostKey.Checked {
		skipWarning.Hide()
	}
	u.skipHostKey.OnChanged = func(checked bool) {
		if checked {
			skipWarning.Show()
		} else {
			skipWarning.Hide()
		}
	}
	detectRow := container.NewVBox(
		container.NewHBox(
			u.secondaryButton("Detectar destino", theme.SearchIcon(), u.detectTarget),
			u.ghostButton("Testar conexão", theme.ConfirmIcon(), u.testSSH),
		),
		vSpace(6),
		u.detectStatus,
		vSpace(4),
		u.testStatus,
	)
	securityCard := u.sectionCard("Detecção e segurança", "Detecte o sistema operacional remoto antes de seguir. Testar a conexão não substitui a detecção.", container.NewVBox(
		u.skipHostKey,
		skipWarning,
		vSpace(8),
		detectRow,
		vSpace(8),
		u.muted("A detecção lê /etc/os-release e uname -m e preenche distro, versão, arquitetura e imagem recomendada.", theme.SizeNameCaptionText),
	))
	disabledBanner := u.banner("SSH está desativado. A sessão apenas baixará os pacotes no host local.", toneInfo)

	updateSSHState := func(enabled bool) {
		if enabled {
			connectionCard.Show()
			jumpCard.Show()
			securityCard.Show()
			disabledBanner.Hide()
			return
		}
		connectionCard.Hide()
		jumpCard.Hide()
		securityCard.Hide()
		disabledBanner.Show()
	}
	u.sshEnabled.OnChanged = updateSSHState
	updateSSHState(u.sshEnabled.Checked)

	advanced := u.ghostButton("Configurar manualmente", theme.SettingsIcon(), func() {
		if u.sshEnabled.Checked && (u.detected == nil || !u.detectedMatchesSSH()) {
			u.targetSelect.ClearSelected()
			u.versionEntry.SetText("")
			u.archEntry.SetText("")
			u.imageEntry.SetText("")
		}
		u.showConfig()
	})
	continueAction := u.primaryButton("Continuar", theme.NavigateNextIcon(), func() {
		if !u.sshEnabled.Checked {
			u.showConfig()
			return
		}
		if strings.TrimSpace(u.sshHost.Text) == "" || strings.TrimSpace(u.sshUser.Text) == "" {
			u.showError(fmt.Errorf("host e usuário SSH são obrigatórios"))
			return
		}
		if _, err := u.buildSSHConfig(); err != nil {
			u.showError(err)
			return
		}
		if !u.manualTarget && (u.detected == nil || !u.detectedMatchesSSH()) {
			u.detectStatus.SetText("Detectando destino...")
			u.detectTarget()
			return
		}
		if strings.TrimSpace(u.versionEntry.Text) == "" || strings.TrimSpace(u.archEntry.Text) == "" {
			u.showError(fmt.Errorf("detecte o destino ou use a configuração avançada"))
			return
		}
		u.showPackages()
	})
	content := container.NewVBox(
		u.sectionCard("Modo de operação", "Ative o SSH para enviar, validar e instalar remotamente.", u.sshEnabled),
		vSpace(14),
		disabledBanner,
		vSpace(10),
		connectionCard, vSpace(14),
		jumpCard, vSpace(14),
		securityCard, vSpace(12),
		container.NewHBox(advanced, layout.NewSpacer(), u.ghostButton("Voltar", theme.NavigateBackIcon(), u.showWelcome), continueAction),
	)
	u.showScreen("Conexão SSH e Jump Hosts", content)
}

func (u *UI) showPackages() {
	if !u.sshEnabled.Checked {
		u.installCheck.SetChecked(false)
		u.installCheck.Disable()
		u.sudoCheck.SetChecked(false)
		u.sudoCheck.Disable()
		u.sudoPassword.Disable()
	} else {
		u.installCheck.Enable()
		u.sudoCheck.Enable()
	}
	u.pkgEntry.SetMinRowsVisible(6)
	resumeObjects := make([]fyne.CanvasObject, 0, 2)
	if u.resumeID > 0 {
		resumeObjects = append(resumeObjects, u.banner(fmt.Sprintf("Retomando sessão #%d por checkpoint seguro. Revise a configuração antes de continuar.", u.resumeID), toneInfo), vSpace(12))
	}
	packageCard := u.sectionCard("Pacotes solicitados", "Um pacote por linha ou separados por vírgula.", container.NewVBox(
		u.pkgEntry,
		vSpace(5),
		u.muted("Os roots aparecem como principais no relatório; dependências baixadas aparecem como dependentes.", theme.SizeNameCaptionText),
	))
	installCard := u.sectionCard("Instalação remota", "A instalação nunca é retomada automaticamente sem nova confirmação.", container.NewVBox(
		u.installCheck,
		vSpace(4),
		u.sudoCheck,
		u.sudoPassword,
	))
	optionsCard := u.sectionCard("Pré-check e opções", "O pré-check remoto evita baixar pacotes que já existem no destino.", container.NewVBox(
		u.optionalCheck,
		vSpace(4),
		u.forceCheck,
	))
	notSSH := u.banner("Sem SSH, os pacotes serão apenas baixados e validados localmente.", toneInfo)
	notSSH.Hide()
	if !u.sshEnabled.Checked {
		notSSH.Show()
		installCard.Hide()
	}
	syncSudo := func(enabled bool) {
		if enabled && u.sshEnabled.Checked {
			u.sudoPassword.Enable()
		} else {
			u.sudoPassword.Disable()
		}
	}
	u.sudoCheck.OnChanged = syncSudo
	syncSudo(u.sudoCheck.Checked)
	note := u.banner("Use “Forçar reinstalação” para ignorar pacotes já presentes. O manifesto SHA256SUMS é gerado antes do envio.", toneInfo)
	content := container.NewVBox(
		container.NewVBox(resumeObjects...),
		packageCard, vSpace(14),
		optionsCard, vSpace(14),
		installCard, vSpace(10),
		notSSH, vSpace(10),
		note, vSpace(8),
		u.nav(u.showSSH, "Iniciar", u.startRun),
	)
	u.showScreen("Pacotes e Pré-check", content)
}

func (u *UI) showProgress() {
	u.resetSteps()
	stepObjects := make([]fyne.CanvasObject, 0, len(u.stepLabels))
	for _, label := range u.stepLabels {
		label.SizeName = theme.SizeNameCaptionText
		stepObjects = append(stepObjects, label)
	}
	steps := container.NewGridWithColumns(2, stepObjects...)
	stepCard := u.sectionCard("Etapas do pipeline", "13 etapas coordenadas pelo núcleo.", steps)
	u.logLabel.TextStyle = fyne.TextStyle{Monospace: true}
	u.logLabel.SizeName = theme.SizeNameCaptionText
	logCard := u.sectionCard("Log em tempo real", "Eventos relevantes da execução.", u.logLabel)
	u.progressBar.SetValue(0)
	u.progressText.SizeName = theme.SizeNameSubHeadingText
	u.progressText.Wrapping = fyne.TextWrapWord
	progressCard := u.sectionCard("Progresso", "Download, integridade e transferência.", container.NewVBox(
		u.progressText,
		vSpace(8),
		u.progressBar,
	))
	content := container.NewVBox(
		progressCard, vSpace(16),
		container.NewGridWithColumns(2, stepCard, logCard), vSpace(14),
		container.NewHBox(layout.NewSpacer(), u.dangerButton("Cancelar execução", theme.CancelIcon(), u.cancelRun)),
	)
	u.showScreen("Pipeline", content)
}

func (u *UI) showResult() {
	title := "Sessão finalizada"
	message := "A operação foi encerrada."
	tone := toneInfo
	if u.runErr != nil {
		title = "Sessão encerrada com erro"
		message = u.runErr.Error()
		tone = toneError
	} else if u.result != nil {
		switch {
		case u.result.InstallDeclined:
			title = "Transferência concluída"
			message = "Os artefatos foram enviados, mas a instalação foi recusada."
			tone = toneWarning
		case u.result.AllAlreadyInstalled:
			title = "Nada a instalar"
			message = "Todos os pacotes solicitados já estavam presentes no destino."
			tone = toneInfo
		case u.result.Installed:
			title = "Instalação concluída"
			message = "Os pacotes foram instalados e confirmados no destino."
			tone = toneSuccess
		default:
			title = "Sessão concluída"
			message = "Os artefatos foram baixados, validados e transferidos."
			tone = toneSuccess
		}
	}

	sessionID := u.sessionID
	if u.result != nil {
		sessionID = u.result.SessaoID
	}
	destination := "local"
	if strings.TrimSpace(u.sshHost.Text) != "" && u.sshEnabled.Checked {
		destination = u.sshHost.Text
	}
	installState := "não instalado"
	if u.result != nil {
		switch {
		case u.result.Installed:
			installState = "instalado"
		case u.result.InstallDeclined:
			installState = "recusado"
		case u.result.AllAlreadyInstalled:
			installState = "já presente"
		default:
			installState = "transferido"
		}
	}
	artifactCount := 0
	if u.result != nil {
		artifactCount = len(u.result.Artifacts)
	}
	stats := container.NewGridWithColumns(2,
		u.statTile("Sessão", fmt.Sprintf("#%d", sessionID), "identificador local", theme.HistoryIcon()),
		u.statTile("Artefatos", strconv.Itoa(artifactCount), "no manifesto", theme.DownloadIcon()),
		u.statTile("Destino", destination, "host de destino", theme.ComputerIcon()),
		u.statTile("Instalação", installState, "resultado final", theme.ConfirmIcon()),
	)

	detailObjects := make([]fyne.CanvasObject, 0, 5)
	if u.result != nil {
		detailObjects = append(detailObjects,
			u.detailRow("Saída local", u.result.OutputDir),
			vSpace(10),
			u.detailRow("Manifesto", u.result.CaminhoManifesto),
			vSpace(10),
			u.detailRow("Destino remoto", u.result.RemoteDir),
		)
		if len(u.result.AlreadyInstalled) > 0 {
			names := make([]string, 0, len(u.result.AlreadyInstalled))
			for _, item := range u.result.AlreadyInstalled {
				names = append(names, item.Name)
			}
			detailObjects = append(detailObjects, vSpace(10), u.detailRow("Já instalados", strings.Join(names, ", ")))
		}
	}
	details := u.sectionCard("Rastreabilidade", "Caminhos e informações úteis para auditoria ou suporte.", container.NewVBox(detailObjects...))
	if len(detailObjects) == 0 {
		details = u.banner("Nenhum artefato foi produzido nesta execução.", toneWarning)
	}

	actions := make([]fyne.CanvasObject, 0, 4)
	if sessionID > 0 {
		actions = append(actions, u.primaryButton("Ver relatório", theme.DocumentIcon(), func() {
			u.reportBack = u.showResult
			u.showReport(sessionID)
		}))
	}
	actions = append(actions,
		u.secondaryButton("Histórico", theme.HistoryIcon(), u.showHistory),
		u.ghostButton("Nova sessão", theme.ContentAddIcon(), u.startNewSession),
	)
	sections := []fyne.CanvasObject{u.heroMessage(title, message, tone), vSpace(16)}
	if sessionID > 0 || u.result != nil {
		sections = append(sections, stats, vSpace(16), details, vSpace(10))
	}
	sections = append(sections, container.NewHBox(actions...))
	u.showScreen("Resultado", container.NewVBox(sections...))
}

func (u *UI) showReport(sessionID int64) {
	if sessionID == 0 {
		sessionID = u.sessionID
	}
	u.reportSessionID = sessionID
	if u.store == nil {
		u.openStore()
	}
	reportText := "SQLite indisponível."
	reportErr := error(nil)
	if u.store != nil {
		relatorio, err := observability.GerarRelatorio(context.Background(), u.store, sessionID)
		if err != nil {
			reportText = err.Error()
			reportErr = err
		} else {
			reportText = relatorio.Markdown()
		}
	} else {
		reportErr = fmt.Errorf("SQLite indisponível")
	}
	var document fyne.CanvasObject
	if reportErr != nil {
		document = u.banner(reportErr.Error(), toneError)
	} else {
		rich := widget.NewRichTextFromMarkdown(reportText)
		rich.Wrapping = fyne.TextWrapWord
		document = rich
	}
	reportCard := u.sectionCard("Documento da sessão", "Markdown renderizado a partir do SQLite local.", document)
	jsonButton := u.secondaryButton("Exportar JSON", theme.DocumentSaveIcon(), func() { u.exportReport("json") })
	mdButton := u.primaryButton("Exportar Markdown", theme.DocumentSaveIcon(), func() { u.exportReport("md") })
	syslogButton := u.ghostButton("Enviar ao Syslog", theme.MailSendIcon(), u.sendReportSyslog)
	content := container.NewVBox(
		reportCard, vSpace(12),
		container.NewHBox(jsonButton, mdButton, syslogButton, layout.NewSpacer(), u.ghostButton("Voltar", theme.NavigateBackIcon(), func() {
			if u.reportBack != nil {
				back := u.reportBack
				u.reportBack = nil
				back()
				return
			}
			u.showResult()
		})),
	)
	u.showScreen("Relatório da Sessão", content)
}

func (u *UI) showHistory() {
	if u.store == nil {
		u.openStore()
	}
	sessoes, err := u.allSessions()
	var rows []fyne.CanvasObject
	if err != nil {
		rows = append(rows, u.banner(err.Error(), toneError))
	} else if len(sessoes) == 0 {
		rows = append(rows, u.sectionCard("Nenhuma sessão registrada", "As operações aparecerão aqui depois da primeira execução.", u.banner("Comece criando uma nova sessão.", toneInfo)))
	} else {
		for _, sessao := range sessoes {
			rows = append(rows, u.sessionCard(sessao, u.showHistory), vSpace(10))
		}
	}
	rows = append(rows, u.nav(u.showWelcome, "Atualizar", u.showHistory))
	u.showScreen("Histórico", container.NewVBox(rows...))
}

func (u *UI) showSettings() {
	pathLabel := u.muted("Arquivo de configuração: "+u.settingsPath, theme.SizeNameCaptionText)
	databaseCard := u.sectionCard("Banco de dados", "Local do arquivo SQLite usado pelo histórico, checkpoints e relatórios.", container.NewVBox(
		widget.NewForm(widget.NewFormItem("Arquivo SQLite", u.dbEntry)),
		vSpace(6),
		u.muted("Alterar o caminho abre um novo banco na próxima atualização da página.", theme.SizeNameCaptionText),
	))
	storageCard := u.sectionCard("Armazenamento temporário", "Usado pelos ambientes isolados durante a preparação dos artefatos.", widget.NewForm(
		widget.NewFormItem("Diretório temporário", u.settingsTemp),
		widget.NewFormItem("Timeout de etapa (segundos)", u.settingsTimeout),
	))
	syslogCard := u.sectionCard("Syslog externo", "Envio best-effort em background via RFC 5424. Falhas não interrompem o pipeline.", widget.NewForm(
		widget.NewFormItem("Habilitar", u.syslogCheck),
		widget.NewFormItem("Host", u.syslogHost),
		widget.NewFormItem("Porta", u.syslogPort),
		widget.NewFormItem("Protocolo", u.syslogProto),
		widget.NewFormItem("Tag", u.syslogTag),
	))
	syncSyslog := func(enabled bool) {
		if enabled {
			u.syslogHost.Enable()
			u.syslogPort.Enable()
			u.syslogProto.Enable()
			u.syslogTag.Enable()
			return
		}
		u.syslogHost.Disable()
		u.syslogPort.Disable()
		u.syslogProto.Disable()
		u.syslogTag.Disable()
	}
	u.syslogCheck.OnChanged = syncSyslog
	syncSyslog(u.syslogCheck.Checked)

	save := func() {
		timeout, err := strconv.Atoi(strings.TrimSpace(u.settingsTimeout.Text))
		if err != nil || timeout <= 0 {
			u.showError(fmt.Errorf("timeout inválido"))
			return
		}
		port, portErr := strconv.Atoi(strings.TrimSpace(u.syslogPort.Text))
		if portErr != nil || port <= 0 || port > 65535 {
			u.showError(fmt.Errorf("porta Syslog inválida"))
			return
		}
		dbPath := strings.TrimSpace(u.dbEntry.Text)
		if dbPath == "" {
			if cwd, err := os.Getwd(); err == nil {
				dbPath = filepath.Join(cwd, "opf.db")
			}
		}
		u.dbEntry.SetText(dbPath)
		u.settings.DatabasePath = dbPath
		u.settings.TempDir = strings.TrimSpace(u.settingsTemp.Text)
		u.settings.StageTimeoutSecs = timeout
		u.settings.SyslogEnabled = u.syslogCheck.Checked
		u.settings.SyslogHost = strings.TrimSpace(u.syslogHost.Text)
		u.settings.SyslogPort = port
		u.settings.SyslogProto = strings.ToLower(strings.TrimSpace(u.syslogProto.Text))
		u.settings.SyslogTag = strings.TrimSpace(u.syslogTag.Text)
		if u.settings.SyslogEnabled && u.settings.SyslogHost == "" {
			u.showError(fmt.Errorf("host Syslog é obrigatório quando habilitado"))
			return
		}
		if err := config.Save(u.settingsPath, u.settings); err != nil {
			u.showError(err)
			return
		}
		u.openStore()
		if u.settingsErr != nil {
			u.showError(u.settingsErr)
			return
		}
		dialog.ShowInformation("Configurações", "Configurações salvas.", u.window)
		u.showSettings()
	}
	note := u.banner("A exportação JSON/Markdown está disponível no relatório de cada sessão.", toneInfo)
	content := container.NewVBox(
		pathLabel, vSpace(12),
		databaseCard, vSpace(12),
		storageCard, vSpace(12),
		syslogCard, vSpace(12),
		note, vSpace(8),
		container.NewHBox(u.ghostButton("Voltar", theme.NavigateBackIcon(), u.showWelcome), layout.NewSpacer(),
			u.primaryButton("Salvar configurações", theme.DocumentSaveIcon(), save)),
	)
	u.showScreen("Configurações Globais", content)
}

func (u *UI) sendReportSyslog() {
	if !u.settings.SyslogEnabled {
		u.showError(fmt.Errorf("habilite o Syslog nas configurações globais"))
		return
	}
	if u.store == nil {
		u.openStore()
	}
	if u.store == nil {
		u.showError(fmt.Errorf("SQLite indisponível"))
		return
	}
	id := u.reportSessionID
	if id == 0 {
		id = u.sessionID
	}
	relatorio, err := observability.GerarRelatorio(context.Background(), u.store, id)
	if err != nil {
		u.showError(err)
		return
	}
	err = observability.SendRelatorioSyslog(context.Background(), observability.SyslogConfig{
		Enabled: true, Host: u.settings.SyslogHost, Port: u.settings.SyslogPort,
		Proto: u.settings.SyslogProto, Tag: u.settings.SyslogTag,
	}, relatorio)
	if err != nil {
		u.showError(err)
		return
	}
	dialog.ShowInformation("Syslog", "Relatório enfileirado para envio best-effort.", u.window)
}

func (u *UI) exportReport(format string) {
	e := u.exportReportError(format)
	if e != nil {
		u.showError(e)
	}
}

func (u *UI) exportReportError(format string) error {
	if u.store == nil {
		return fmt.Errorf("SQLite indisponível")
	}
	id := u.reportSessionID
	if id == 0 {
		id = u.sessionID
	}
	relatorio, err := observability.GerarRelatorio(context.Background(), u.store, id)
	if err != nil {
		return err
	}
	dir := strings.TrimSpace(u.outEntry.Text)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	name := fmt.Sprintf("opf-relatorio-%d", id)
	var path string
	switch format {
	case "json":
		path = filepath.Join(dir, name+".json")
		err = observability.SalvarJSON(path, relatorio)
	case "md":
		path = filepath.Join(dir, name+".md")
		err = observability.SalvarMarkdown(path, relatorio)
	default:
		return fmt.Errorf("formato de relatório inválido")
	}
	if err != nil {
		return err
	}
	dialog.ShowInformation("Relatório", "Arquivo gerado: "+path, u.window)
	return nil
}

func (u *UI) continueLatest() {
	if u.store == nil {
		u.openStore()
	}
	if u.store == nil {
		u.showError(fmt.Errorf("SQLite indisponível"))
		return
	}
	sessoes, err := u.store.ListarSessoes(context.Background(), true)
	if err != nil {
		u.showError(err)
		return
	}
	for _, sessao := range sessoes {
		if sessao.Status != core.SessaoConcluida {
			u.resumeSession(sessao.ID)
			return
		}
	}
	dialog.ShowInformation("Continuar", "Nenhuma sessão inacabada encontrada.", u.window)
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
	SSHJumps      []opfapp.SSHJumpMetadata `json:"ssh_jumps"`
	Repositories  []core.PackageRepository `json:"repositories"`
	Install       bool                     `json:"install"`
}

func (u *UI) resumeSession(id int64) {
	if u.store == nil {
		u.openStore()
	}
	if u.store == nil {
		u.showError(fmt.Errorf("SQLite indisponível"))
		return
	}
	sessao, err := u.store.ObterSessao(context.Background(), id)
	if err != nil {
		u.showError(err)
		return
	}
	var cfg sessionConfigJSON
	if sessao.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(sessao.ConfigJSON), &cfg)
	}
	u.resumeID = id
	u.sessionID = id
	u.manualTarget = true
	u.detected = nil
	u.detectedHost = ""
	u.detectedUser = ""
	u.detectedPort = ""
	u.detectedJumps = ""
	if cfg.TargetOS != "" {
		switch cfg.TargetOS {
		case "debian":
			u.targetSelect.SetSelected("Debian")
		case "centos":
			u.targetSelect.SetSelected("CentOS")
		case "alpine":
			u.targetSelect.SetSelected("Alpine")
		default:
			u.targetSelect.SetSelected("Ubuntu")
		}
	}
	if cfg.TargetVersion != "" {
		u.versionEntry.SetText(cfg.TargetVersion)
	}
	if cfg.TargetArch != "" {
		u.archEntry.SetText(cfg.TargetArch)
	}
	u.setRepositories(cfg.Repositories)
	if cfg.FetchMethod != "" {
		switch cfg.FetchMethod {
		case "wsl":
			u.methodSelect.SetSelected("WSL")
		case "auto":
			u.methodSelect.SetSelected("Automático")
		default:
			u.methodSelect.SetSelected("Docker")
		}
	}
	u.imageEntry.SetText(cfg.Image)
	u.wslEntry.SetText(cfg.WSLDistro)
	u.remoteDir.SetText(cfg.RemoteDir)
	if sessao.RemoteDir != "" && cfg.RemoteDir == "" {
		u.remoteDir.SetText(sessao.RemoteDir)
	}
	if cfg.SSHHost != "" {
		u.sshEnabled.SetChecked(true)
		u.sshHost.SetText(cfg.SSHHost)
		u.sshUser.SetText(cfg.SSHUser)
		if cfg.SSHPort > 0 {
			u.sshPort.SetText(strconv.Itoa(cfg.SSHPort))
		}
	} else if sessao.HostDestino != "" {
		u.sshEnabled.SetChecked(true)
		u.sshHost.SetText(sessao.HostDestino)
	}
	needsJumpPassword := u.setJumpMetadata(cfg.SSHJumps)
	u.installCheck.SetChecked(cfg.Install)
	if len(cfg.Packages) > 0 {
		u.pkgEntry.SetText(strings.Join(cfg.Packages, "\n"))
	} else if solicitados, err := u.store.ListarPacotesSolicitados(context.Background(), id); err == nil {
		var names []string
		for _, p := range solicitados {
			names = append(names, p.Nome)
		}
		u.pkgEntry.SetText(strings.Join(names, "\n"))
	}
	message := "Revise a configuração e reautentique antes de continuar."
	if needsJumpPassword {
		message += "\nAs senhas das pontes não são persistidas; informe-as novamente."
	}
	dialog.ShowInformation("Retomar Sessão", message, u.window)
	u.showSSH()
}
