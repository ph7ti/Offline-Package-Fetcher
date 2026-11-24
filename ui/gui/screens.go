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
	"opf/internal/i18n"
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
	quickActions := u.sectionCard(i18n.T("Ações rápidas"), i18n.T("Inicie uma operação ou consulte o histórico local."), container.NewHBox(
		u.primaryButton(i18n.T("Nova sessão"), theme.ContentAddIcon(), u.startNewSession),
		u.secondaryButton(i18n.T("Continuar"), theme.MediaPlayIcon(), u.continueLatest),
		u.ghostButton(i18n.T("Histórico"), theme.HistoryIcon(), u.showHistory),
		u.ghostButton(i18n.T("Configurações"), theme.SettingsIcon(), u.showSettings),
		u.ghostButton(i18n.T("Atualizar"), theme.ViewRefreshIcon(), u.showWelcome),
	))
	stats := container.NewGridWithColumns(2,
		u.statTile(i18n.T("Sessões"), strconv.Itoa(len(sessoes)), i18n.T("registradas localmente"), theme.HistoryIcon()),
		u.statTile(i18n.T("Pacotes"), strconv.Itoa(totalPackages), i18n.T("artefatos rastreados"), theme.DownloadIcon()),
		u.statTile(i18n.T("Instalados"), strconv.Itoa(totalInstalled), i18n.T("confirmados no destino"), theme.ConfirmIcon()),
		u.statTile(i18n.T("Banco de dados"), "SQLite", dbName, theme.StorageIcon()),
	)

	recentObjects := make([]fyne.CanvasObject, 0, 6)
	if sessionErr != nil {
		recentObjects = append(recentObjects, u.banner(sessionErr.Error(), toneError))
	} else if len(sessoes) == 0 {
		recentObjects = append(recentObjects, u.banner(i18n.T("Nenhuma sessão registrada ainda. Comece com uma nova sessão."), toneInfo))
	} else {
		limit := len(sessoes)
		if limit > 4 {
			limit = 4
		}
		for _, sessao := range sessoes[:limit] {
			recentObjects = append(recentObjects, u.sessionCard(sessao, u.showWelcome), vSpace(8))
		}
	}
	recentObjects = append(recentObjects, u.ghostButton(i18n.T("Ver histórico completo"), theme.HistoryIcon(), u.showHistory))
	recent := u.sectionCard(i18n.T("Sessões recentes"), i18n.T("Abra o relatório, retome uma operação interrompida ou audite os últimos resultados."), container.NewVBox(recentObjects...))

	u.showScreen("Início", container.NewVBox(quickActions, vSpace(12), stats, vSpace(12), recent))
}
func (u *UI) allSessions() ([]core.SessaoResumo, error) {
	if u.store == nil {
		return nil, fmt.Errorf("%s", i18n.T("SQLite indisponível"))
	}
	return u.store.ListarSessoes(context.Background(), true)
}

func (u *UI) showConfig() {
	u.refreshMethodSelect()
	targetForm := widget.NewForm(
		widget.NewFormItem(i18n.T("Distro de destino"), u.targetSelect),
		widget.NewFormItem(i18n.T("Versão"), u.versionEntry),
		widget.NewFormItem(i18n.T("Arquitetura"), u.archEntry),
	)
	environmentForm := widget.NewForm(
		widget.NewFormItem(i18n.T("Método de fetch"), u.methodSelect),
		widget.NewFormItem(i18n.T("Imagem Docker (opcional)"), u.imageEntry),
		widget.NewFormItem(i18n.T("Distro WSL (opcional)"), u.wslEntry),
	)
	storageForm := widget.NewForm(
		widget.NewFormItem(i18n.T("Diretório de saída"), u.outEntry),
	)
	targetCard := u.sectionCard(i18n.T("Sistema de destino"), i18n.T("O pipeline baixa artefatos compatíveis com este alvo."), targetForm)
	environmentCard := u.sectionCard(i18n.T("Ambiente de download"), i18n.T("Docker é o caminho validado; WSL permanece disponível para famílias APT."), environmentForm)
	u.repoList = container.NewVBox()
	addRepo := u.secondaryButton(i18n.T("Adicionar repositório"), theme.ContentAddIcon(), u.addRepoRow)
	repoCard := u.sectionCard(i18n.T("Repositórios adicionais"), i18n.T("Usados apenas no ambiente isolado de download; a lista é aditiva e não substitui os repositórios oficiais."), container.NewVBox(
		u.muted(i18n.T("Família atual: %s. Nenhuma credencial de repositório é armazenada.", u.repositoryFamilyOrApt()), theme.SizeNameCaptionText),
		vSpace(8),
		u.repoList,
		vSpace(8),
		container.NewHBox(addRepo, layout.NewSpacer()),
	))
	u.refreshRepoList()
	optionsCard := u.sectionCard(i18n.T("Saída e opções"), i18n.T("Os artefatos e o manifesto SHA256SUMS ficam no diretório de saída. O banco é definido em Configurações Globais."), container.NewVBox(
		storageForm,
		vSpace(8),
		u.optionalCheck,
		u.offlineBundleCheck,
	))
	u.refreshProfiles()
	profileCard := u.sectionCard(
		i18n.T("Perfis salvos"),
		i18n.T("Reutilize configurações não sensíveis entre sessões. Nenhuma senha ou credencial é salva."),
		container.NewVBox(
			widget.NewForm(
				widget.NewFormItem(i18n.T("Perfil"), u.profileSelect),
				widget.NewFormItem(i18n.T("Nome"), u.profileNameEntry),
			),
			container.NewHBox(
				u.secondaryButton(i18n.T("Carregar perfil"), theme.FolderOpenIcon(), u.loadSelectedProfile),
				u.primaryButton(i18n.T("Salvar perfil"), theme.DocumentSaveIcon(), u.saveCurrentProfile),
				u.ghostButton(i18n.T("Excluir perfil"), theme.DeleteIcon(), u.deleteSelectedProfile),
				layout.NewSpacer(),
			),
		),
	)
	note := u.banner(i18n.T("Presets validados: Ubuntu 22.04/24.04, Debian 12, CentOS 7, Alpine 3.20 e famílias RPM recentes. “Automático” usa WSL compatível no Windows e Docker nos demais casos."), toneInfo)
	content := container.NewVBox(
		profileCard, vSpace(14),
		targetCard, vSpace(14),
		environmentCard, vSpace(14),
		repoCard, vSpace(14),
		optionsCard, vSpace(12),
		note, vSpace(8),
		u.nav(u.showSSH, i18n.T("Continuar"), func() {
			if u.targetSelect.Selected == "" || strings.TrimSpace(u.versionEntry.Text) == "" || strings.TrimSpace(u.archEntry.Text) == "" {
				u.showError(fmt.Errorf("%s", i18n.T("distro, versão e arquitetura são obrigatórias")))
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
	u.testStatus = widget.NewLabel(i18n.T("Teste de conexão: não executado"))
	u.testStatus.SizeName = theme.SizeNameCaptionText
	u.testStatus.Wrapping = fyne.TextWrapWord
	u.detectStatus.SizeName = theme.SizeNameCaptionText
	u.detectStatus.Wrapping = fyne.TextWrapWord
	if u.detected != nil && !u.detectedMatchesSSH() {
		u.detectStatus.SetText(i18n.T("Configuração SSH alterada; detecte novamente."))
	}

	importRow := container.NewBorder(nil, nil, nil,
		u.secondaryButton(i18n.T("Importar"), theme.DownloadIcon(), u.importSSHConfigAlias),
		u.sshConfigAlias,
	)
	connectionCard := u.sectionCard(i18n.T("Conexão"), i18n.T("Use chave ou senha. A host key é verificada estritamente por padrão."), widget.NewForm(
		widget.NewFormItem(i18n.T("Importar ~/.ssh/config"), importRow),
		widget.NewFormItem(i18n.T("Host do destino"), u.sshHost),
		widget.NewFormItem(i18n.T("Porta"), u.sshPort),
		widget.NewFormItem(i18n.T("Usuário"), u.sshUser),
		widget.NewFormItem(i18n.T("Chave privada"), u.sshKey),
		widget.NewFormItem(i18n.T("Passphrase da chave"), u.sshKeyPassphrase),
		widget.NewFormItem(i18n.T("Agente SSH"), u.sshUseAgent),
		widget.NewFormItem(i18n.T("Senha da conta"), u.sshPassword),
		widget.NewFormItem("Known hosts", u.knownHosts),
	))
	u.jumpList = container.NewVBox()
	addJump := u.secondaryButton(i18n.T("Adicionar ponte"), theme.ContentAddIcon(), u.addJumpRow)
	jumpCard := u.sectionCard(i18n.T("Pontes SSH e destino remoto"), i18n.T("As pontes são abertas em ordem; cada uma atravessa a anterior e usa credenciais próprias."), container.NewVBox(
		u.muted(i18n.T("Senhas das pontes ficam somente em memória e não são restauradas ao retomar; sessões antigas podem exigir reconfigurar as pontes."), theme.SizeNameCaptionText),
		vSpace(8),
		u.jumpList,
		vSpace(8),
		container.NewHBox(addJump, layout.NewSpacer()),
		vSpace(8),
		widget.NewForm(
			widget.NewFormItem(i18n.T("Diretório remoto"), u.remoteDir),
		),
	))
	u.refreshJumpList()
	skipWarning := u.banner(i18n.T("Host key ignorada: a conexão fica vulnerável a MITM. Use apenas em ambiente controlado."), toneError)
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
			u.secondaryButton(i18n.T("Detectar destino"), theme.SearchIcon(), u.detectTarget),
			u.ghostButton(i18n.T("Testar conexão"), theme.ConfirmIcon(), u.testSSH),
		),
		vSpace(6),
		u.detectStatus,
		vSpace(4),
		u.testStatus,
	)
	securityCard := u.sectionCard(i18n.T("Detecção e segurança"), i18n.T("Detecte o sistema operacional remoto antes de seguir. Testar a conexão não substitui a detecção."), container.NewVBox(
		u.skipHostKey,
		skipWarning,
		vSpace(8),
		detectRow,
		vSpace(8),
		u.muted(i18n.T("A detecção lê /etc/os-release e uname -m e preenche distro, versão, arquitetura e imagem recomendada."), theme.SizeNameCaptionText),
	))
	disabledBanner := u.banner(i18n.T("SSH está desativado. A sessão apenas baixará os pacotes no host local."), toneInfo)

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

	advanced := u.ghostButton(i18n.T("Configurar manualmente"), theme.SettingsIcon(), func() {
		if u.sshEnabled.Checked && (u.detected == nil || !u.detectedMatchesSSH()) {
			u.targetSelect.ClearSelected()
			u.versionEntry.SetText("")
			u.archEntry.SetText("")
			u.imageEntry.SetText("")
		}
		u.showConfig()
	})
	continueAction := u.primaryButton(i18n.T("Continuar"), theme.NavigateNextIcon(), func() {
		if !u.sshEnabled.Checked {
			u.showConfig()
			return
		}
		if strings.TrimSpace(u.sshHost.Text) == "" || strings.TrimSpace(u.sshUser.Text) == "" {
			u.showError(fmt.Errorf("%s", i18n.T("host e usuário SSH são obrigatórios")))
			return
		}
		if _, err := u.buildSSHConfig(); err != nil {
			u.showError(err)
			return
		}
		if !u.manualTarget && (u.detected == nil || !u.detectedMatchesSSH()) {
			u.detectStatus.SetText(i18n.T("Detectando destino..."))
			u.detectTarget()
			return
		}
		if strings.TrimSpace(u.versionEntry.Text) == "" || strings.TrimSpace(u.archEntry.Text) == "" {
			u.showError(fmt.Errorf("%s", i18n.T("detecte o destino ou use a configuração avançada")))
			return
		}
		u.showPackages()
	})
	content := container.NewVBox(
		u.sectionCard(i18n.T("Modo de operação"), i18n.T("Ative o SSH para enviar, validar e instalar remotamente."), u.sshEnabled),
		vSpace(14),
		disabledBanner,
		vSpace(10),
		connectionCard, vSpace(14),
		jumpCard, vSpace(14),
		securityCard, vSpace(12),
		container.NewHBox(advanced, layout.NewSpacer(), u.ghostButton(i18n.T("Voltar"), theme.NavigateBackIcon(), u.showWelcome), continueAction),
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
		u.syncVersionReportSelect()
	} else {
		u.installCheck.Enable()
		u.sudoCheck.Enable()
		u.syncVersionReportSelect()
	}
	u.pkgEntry.SetMinRowsVisible(6)
	resumeObjects := make([]fyne.CanvasObject, 0, 2)
	if u.resumeID > 0 {
		resumeObjects = append(resumeObjects, u.banner(i18n.T("Retomando sessão #%d por checkpoint seguro. Revise a configuração antes de continuar.", u.resumeID), toneInfo), vSpace(12))
	}
	packageCard := u.sectionCard(i18n.T("Pacotes solicitados"), i18n.T("Um pacote por linha ou separados por vírgula."), container.NewVBox(
		u.pkgEntry,
		vSpace(5),
		u.muted(i18n.T("Os roots aparecem como principais no relatório; dependências baixadas aparecem como dependentes."), theme.SizeNameCaptionText),
	))
	installCard := u.sectionCard(i18n.T("Instalação remota"), i18n.T("A instalação nunca é retomada automaticamente sem nova confirmação."), container.NewVBox(
		u.installCheck,
		vSpace(4),
		u.sudoCheck,
		u.sudoPassword,
	))
	optionsCard := u.sectionCard(i18n.T("Pré-check e opções"), i18n.T("O pré-check remoto evita baixar pacotes que já existem no destino."), container.NewVBox(
		u.optionalCheck,
		vSpace(4),
		u.forceCheck,
		vSpace(8),
		u.muted(i18n.T("Coletar versões do pacote"), theme.SizeNameSubHeadingText),
		u.versionReportGroup,
		u.muted(i18n.T("Todos: inventário completo; Default: pacotes afetados e dependências; Nenhum: sem coleta e sem tela Package diff."), theme.SizeNameCaptionText),
	))
	notSSH := u.banner(i18n.T("Sem SSH, os pacotes serão apenas baixados e validados localmente."), toneInfo)
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
	note := u.banner(i18n.T("Use “Forçar reinstalação” para ignorar pacotes já presentes. A coleta de versões exige SSH e acontece antes do download; o manifesto SHA256SUMS é gerado antes do envio."), toneInfo)
	content := container.NewVBox(
		container.NewVBox(resumeObjects...),
		packageCard, vSpace(14),
		optionsCard, vSpace(14),
		installCard, vSpace(10),
		notSSH, vSpace(10),
		note, vSpace(8),
		u.nav(u.showSSH, i18n.T("Iniciar"), u.startRun),
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
	stepCard := u.sectionCard(i18n.T("Etapas do pipeline"), i18n.T("13 etapas coordenadas pelo núcleo."), steps)
	u.logLabel.TextStyle = fyne.TextStyle{Monospace: true}
	u.logLabel.SizeName = theme.SizeNameCaptionText
	logCard := u.sectionCard(i18n.T("Log em tempo real"), i18n.T("Eventos relevantes da execução."), u.logLabel)
	u.progressBar.SetValue(0)
	u.progressText.SizeName = theme.SizeNameSubHeadingText
	u.progressText.Wrapping = fyne.TextWrapWord
	progressCard := u.sectionCard(i18n.T("Progresso"), i18n.T("Download, integridade e transferência."), container.NewVBox(
		u.progressText,
		vSpace(8),
		u.progressBar,
	))
	content := container.NewVBox(
		progressCard, vSpace(16),
		container.NewGridWithColumns(2, stepCard, logCard), vSpace(14),
		container.NewHBox(layout.NewSpacer(), u.dangerButton(i18n.T("Cancelar execução"), theme.CancelIcon(), u.cancelRun)),
	)
	u.showScreen("Pipeline", content)
}

func (u *UI) showResult() {
	title := i18n.T("Sessão finalizada")
	message := i18n.T("A operação foi encerrada.")
	tone := toneInfo
	if u.runErr != nil {
		title = i18n.T("Sessão encerrada com erro")
		message = u.runErr.Error()
		tone = toneError
	} else if u.result != nil {
		switch {
		case u.result.InstallDeclined:
			title = i18n.T("Transferência concluída")
			message = i18n.T("Os artefatos foram enviados, mas a instalação foi recusada.")
			tone = toneWarning
		case u.result.AllAlreadyInstalled:
			title = i18n.T("Nada a instalar")
			message = i18n.T("Todos os pacotes solicitados já estavam presentes no destino.")
			tone = toneInfo
		case u.result.Installed:
			title = i18n.T("Instalação concluída")
			message = i18n.T("Os pacotes foram instalados e confirmados no destino.")
			tone = toneSuccess
		default:
			title = i18n.T("Sessão concluída")
			message = i18n.T("Os artefatos foram baixados, validados e transferidos.")
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
	installState := i18n.T("não instalado")
	if u.result != nil {
		switch {
		case u.result.Installed:
			installState = i18n.T("instalado")
		case u.result.InstallDeclined:
			installState = i18n.T("recusado")
		case u.result.AllAlreadyInstalled:
			installState = i18n.T("já presente")
		default:
			installState = i18n.T("transferido")
		}
	}
	artifactCount := 0
	if u.result != nil {
		artifactCount = len(u.result.Artifacts)
	}
	stats := container.NewGridWithColumns(2,
		u.statTile(i18n.T("Sessão"), fmt.Sprintf("#%d", sessionID), i18n.T("identificador local"), theme.HistoryIcon()),
		u.statTile(i18n.T("Artefatos"), strconv.Itoa(artifactCount), i18n.T("no manifesto"), theme.DownloadIcon()),
		u.statTile(i18n.T("Destino"), destination, i18n.T("host de destino"), theme.ComputerIcon()),
		u.statTile(i18n.T("Instalação"), installState, i18n.T("resultado final"), theme.ConfirmIcon()),
	)

	detailObjects := make([]fyne.CanvasObject, 0, 5)
	if u.result != nil {
		detailObjects = append(detailObjects,
			u.detailRow(i18n.T("Saída local"), u.result.OutputDir),
			vSpace(10),
			u.detailRow(i18n.T("Manifesto"), u.result.CaminhoManifesto),
			vSpace(10),
			u.detailRow(i18n.T("Destino remoto"), u.result.RemoteDir),
		)
		if u.result.OfflineBundlePath != "" {
			detailObjects = append(detailObjects, vSpace(10), u.detailRow(i18n.T("Bundle offline"), u.result.OfflineBundlePath))
		}
		if len(u.result.AlreadyInstalled) > 0 {
			names := make([]string, 0, len(u.result.AlreadyInstalled))
			for _, item := range u.result.AlreadyInstalled {
				names = append(names, item.Name)
			}
			detailObjects = append(detailObjects, vSpace(10), u.detailRow(i18n.T("Já instalados"), strings.Join(names, ", ")))
		}
	}
	details := u.sectionCard(i18n.T("Rastreabilidade"), i18n.T("Caminhos e informações úteis para auditoria ou suporte."), container.NewVBox(detailObjects...))
	if len(detailObjects) == 0 {
		details = u.banner(i18n.T("Nenhum artefato foi produzido nesta execução."), toneWarning)
	}

	actions := make([]fyne.CanvasObject, 0, 4)
	if sessionID > 0 {
		actions = append(actions, u.primaryButton(i18n.T("Ver relatório"), theme.DocumentIcon(), func() {
			u.reportBack = u.showResult
			u.showReport(sessionID)
		}))
	}
	actions = append(actions,
		u.secondaryButton(i18n.T("Histórico"), theme.HistoryIcon(), u.showHistory),
		u.ghostButton(i18n.T("Nova sessão"), theme.ContentAddIcon(), u.startNewSession),
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
	reportText := i18n.T("SQLite indisponível.")
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
		reportErr = fmt.Errorf("%s", i18n.T("SQLite indisponível"))
	}
	var document fyne.CanvasObject
	if reportErr != nil {
		document = u.banner(reportErr.Error(), toneError)
	} else {
		rich := widget.NewRichTextFromMarkdown(reportText)
		rich.Wrapping = fyne.TextWrapWord
		document = rich
	}
	reportCard := u.sectionCard(i18n.T("Documento da sessão"), i18n.T("Markdown renderizado a partir do SQLite local."), document)
	jsonButton := u.secondaryButton(i18n.T("Exportar JSON"), theme.DocumentSaveIcon(), func() { u.exportReport("json") })
	mdButton := u.primaryButton(i18n.T("Exportar Markdown"), theme.DocumentSaveIcon(), func() { u.exportReport("md") })
	syslogButton := u.ghostButton(i18n.T("Enviar ao Syslog"), theme.MailSendIcon(), u.sendReportSyslog)
	content := container.NewVBox(
		reportCard, vSpace(12),
		container.NewHBox(jsonButton, mdButton, syslogButton, layout.NewSpacer(), u.ghostButton(i18n.T("Voltar"), theme.NavigateBackIcon(), func() {
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
		rows = append(rows, u.sectionCard(i18n.T("Nenhuma sessão registrada"), i18n.T("As operações aparecerão aqui depois da primeira execução."), u.banner(i18n.T("Comece criando uma nova sessão."), toneInfo)))
	} else {
		for _, sessao := range sessoes {
			rows = append(rows, u.sessionCard(sessao, u.showHistory), vSpace(10))
		}
	}
	rows = append(rows, u.nav(u.showWelcome, i18n.T("Atualizar"), u.showHistory))
	u.showScreen("Histórico", container.NewVBox(rows...))
}

func (u *UI) showSettings() {
	u.syncLanguageSelect()
	pathLabel := u.muted(i18n.T("Arquivo de configuração: %s", u.settingsPath), theme.SizeNameCaptionText)
	languageCard := u.sectionCard(i18n.T("Idioma da interface"), i18n.T("Idioma usado pelos textos da interface. Automático segue o sistema."), container.NewVBox(
		widget.NewForm(widget.NewFormItem(i18n.T("Idioma"), u.languageSelect)),
		vSpace(6),
		u.muted(i18n.T("A alteração é aplicada ao salvar."), theme.SizeNameCaptionText),
	))
	databaseCard := u.sectionCard(i18n.T("Banco de dados"), i18n.T("Local do arquivo SQLite usado pelo histórico, checkpoints e relatórios."), container.NewVBox(
		widget.NewForm(widget.NewFormItem(i18n.T("Arquivo SQLite"), u.dbEntry)),
		vSpace(6),
		u.muted(i18n.T("Alterar o caminho abre um novo banco na próxima atualização da página."), theme.SizeNameCaptionText),
	))
	storageCard := u.sectionCard(i18n.T("Armazenamento temporário"), i18n.T("Usado pelos ambientes isolados durante a preparação dos artefatos."), widget.NewForm(
		widget.NewFormItem(i18n.T("Diretório temporário"), u.settingsTemp),
		widget.NewFormItem(i18n.T("Timeout de etapa (segundos)"), u.settingsTimeout),
	))
	runtimeCard := u.sectionCard(i18n.T("Runtime de contêiner"), i18n.T("Endpoint opcional para Podman ou Docker rootless (vazio usa detecção automática)."), widget.NewForm(
		widget.NewFormItem(i18n.T("Container host (Docker/Podman)"), u.settingsContainerHost),
	))
	proxyCard := u.sectionCard(i18n.T("Proxy corporativo e CA"), i18n.T("Aplicado no contêiner isolado. Credenciais embutidas na URL do proxy não são persistidas."), widget.NewForm(
		widget.NewFormItem("HTTP_PROXY", u.settingsHTTPProxy),
		widget.NewFormItem("HTTPS_PROXY", u.settingsHTTPSProxy),
		widget.NewFormItem("NO_PROXY", u.settingsNoProxy),
		widget.NewFormItem(i18n.T("CA corporativa (PEM)"), u.settingsCACert),
	))
	syslogCard := u.sectionCard(i18n.T("Syslog externo"), i18n.T("Envio best-effort em background via RFC 5424. Falhas não interrompem o pipeline."), widget.NewForm(
		widget.NewFormItem(i18n.T("Habilitar"), u.syslogCheck),
		widget.NewFormItem("Host", u.syslogHost),
		widget.NewFormItem(i18n.T("Porta"), u.syslogPort),
		widget.NewFormItem(i18n.T("Protocolo"), u.syslogProto),
		widget.NewFormItem(i18n.T("Tag"), u.syslogTag),
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
			u.showError(fmt.Errorf("%s", i18n.T("timeout inválido")))
			return
		}
		port, portErr := strconv.Atoi(strings.TrimSpace(u.syslogPort.Text))
		if portErr != nil || port <= 0 || port > 65535 {
			u.showError(fmt.Errorf("%s", i18n.T("porta Syslog inválida")))
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
		u.settings.ContainerHost = strings.TrimSpace(u.settingsContainerHost.Text)
		u.settings.HTTPProxy = strings.TrimSpace(u.settingsHTTPProxy.Text)
		u.settings.HTTPSProxy = strings.TrimSpace(u.settingsHTTPSProxy.Text)
		u.settings.NoProxy = strings.TrimSpace(u.settingsNoProxy.Text)
		u.settings.CACertFile = strings.TrimSpace(u.settingsCACert.Text)
		u.settings.SyslogEnabled = u.syslogCheck.Checked
		u.settings.SyslogHost = strings.TrimSpace(u.syslogHost.Text)
		u.settings.SyslogPort = port
		u.settings.SyslogProto = strings.ToLower(strings.TrimSpace(u.syslogProto.Text))
		u.settings.SyslogTag = strings.TrimSpace(u.syslogTag.Text)
		u.settings.Language = languageSetting(u.languageSelect.Selected)
		if u.settings.SyslogEnabled && u.settings.SyslogHost == "" {
			u.showError(fmt.Errorf("%s", i18n.T("host Syslog é obrigatório quando habilitado")))
			return
		}
		if err := config.Save(u.settingsPath, u.settings); err != nil {
			u.showError(err)
			return
		}
		i18n.SetFromSetting(u.settings.Language)
		u.openStore()
		if u.settingsErr != nil {
			u.showError(u.settingsErr)
			return
		}
		dialog.ShowInformation(i18n.T("Configurações"), i18n.T("Configurações salvas."), u.window)
		u.showSettings()
	}
	note := u.banner(i18n.T("A exportação JSON/Markdown está disponível no relatório de cada sessão."), toneInfo)
	content := container.NewVBox(
		pathLabel, vSpace(12),
		languageCard, vSpace(12),
		databaseCard, vSpace(12),
		storageCard, vSpace(12),
		runtimeCard, vSpace(12),
		proxyCard, vSpace(12),
		syslogCard, vSpace(12),
		note, vSpace(8),
		container.NewHBox(u.ghostButton(i18n.T("Voltar"), theme.NavigateBackIcon(), u.showWelcome), layout.NewSpacer(),
			u.primaryButton(i18n.T("Salvar configurações"), theme.DocumentSaveIcon(), save)),
	)
	u.showScreen("Configurações Globais", content)
}

func (u *UI) sendReportSyslog() {
	if !u.settings.SyslogEnabled {
		u.showError(fmt.Errorf("%s", i18n.T("habilite o Syslog nas configurações globais")))
		return
	}
	if u.store == nil {
		u.openStore()
	}
	if u.store == nil {
		u.showError(fmt.Errorf("%s", i18n.T("SQLite indisponível")))
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
	dialog.ShowInformation("Syslog", i18n.T("Relatório enfileirado para envio best-effort."), u.window)
}

func (u *UI) exportReport(format string) {
	e := u.exportReportError(format)
	if e != nil {
		u.showError(e)
	}
}

func (u *UI) exportReportError(format string) error {
	if u.store == nil {
		return fmt.Errorf("%s", i18n.T("SQLite indisponível"))
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
		return fmt.Errorf("%s", i18n.T("formato de relatório inválido"))
	}
	if err != nil {
		return err
	}
	dialog.ShowInformation(i18n.T("Relatório"), i18n.T("Arquivo gerado: %s", path), u.window)
	return nil
}

func (u *UI) continueLatest() {
	if u.store == nil {
		u.openStore()
	}
	if u.store == nil {
		u.showError(fmt.Errorf("%s", i18n.T("SQLite indisponível")))
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
	dialog.ShowInformation(i18n.T("Continuar"), i18n.T("Nenhuma sessão inacabada encontrada."), u.window)
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
		u.showError(fmt.Errorf("%s", i18n.T("SQLite indisponível")))
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
		u.methodSelect.Options = methodOptions()
		u.methodSelect.SetSelected(methodLabel(cfg.FetchMethod))
		u.methodSelect.Refresh()
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
	message := i18n.T("Revise a configuração e reautentique antes de continuar.")
	if needsJumpPassword {
		message += "\n" + i18n.T("As senhas das pontes não são persistidas; informe-as novamente.")
	}
	dialog.ShowInformation(i18n.T("Retomar Sessão"), message, u.window)
	u.showSSH()
}
