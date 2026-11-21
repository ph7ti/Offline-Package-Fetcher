package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"opf/internal/core"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	helpStyle     = lipgloss.NewStyle().Faint(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m *Model) View() string {
	var body string
	switch m.screen {
	case screenWelcome:
		body = m.viewWelcome()
	case screenConfig:
		body = m.viewForm("Configuração da Sessão", "Presets validados: ubuntu 22.04/24.04, debian 12, centos 7 (imagem preparada) e alpine 3.20. O método auto usa WSL compatível no Windows e Docker nos demais casos.")
	case screenRepos:
		body = m.viewRepos()
	case screenRepoEdit:
		body = m.viewForm("Repositório adicional", "Usado somente no ambiente isolado de download; a lista é aditiva e não substitui os repositórios oficiais. APK aceita CA customizada, mas não insecure TLS.")
	case screenSSH:
		body = m.viewForm("Conexão SSH e Jump Hosts", "Conecte primeiro; Enter/ctrl+d detecta /etc/os-release e uname. Host key é estrita por padrão; o toggle “Ignorar” equivale a StrictHostKeyChecking=no e é inseguro.")
		if m.detectedText != "" && m.detectedMatchesSSH() {
			body += "\nDestino detectado: " + m.detectedText + "\n"
		} else if m.sshEnabled && !m.manualTarget {
			body += "\nDestino ainda não detectado ou configuração alterada: Enter/ctrl+d executa a detecção.\n"
		}
	case screenJumps:
		body = m.viewJumps()
	case screenJumpEdit:
		body = m.viewForm("Ponte SSH", "Cada ponte usa credenciais próprias; senhas ficam apenas em memória e nunca são persistidas. Vazio em known_hosts herda o valor do destino.")
	case screenPackages:
		body = m.viewForm("Pacotes e Pré-check", "Pré-check remoto automático; use Forçar reinstalação para ignorar pacotes presentes. A instalação nunca é retomada automaticamente.")
	case screenProgress:
		body = m.viewProgress()
	case screenResult:
		body = m.viewResult()
	case screenReport:
		body = m.viewReport()
	case screenHistory:
		body = m.viewHistory()
	case screenSettings:
		body = m.viewSettings()
	}
	header := titleStyle.Render("OPF TUI 1.1") + "  " + helpStyle.Render("terminal • CGO_ENABLED=0")
	help := helpStyle.Render(m.helpText())
	return header + "\n" + strings.Repeat("─", 72) + "\n\n" + body + "\n\n" + help
}

func (m *Model) viewWelcome() string {
	options := []string{"Nova sessão", "Continuar sessão", "Histórico", "Configurações globais", "Sair"}
	var b strings.Builder
	b.WriteString("Offline Package Fetcher\n\n")
	for i, option := range options {
		prefix := "  "
		if i == m.menuIdx {
			prefix = "> "
			option = selectedStyle.Render(option)
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, option)
	}
	b.WriteString("\n")
	if m.store == nil {
		b.WriteString("SQLite indisponível\n")
	} else {
		b.WriteString("SQLite: OK\n")
	}
	b.WriteString("Sem NOPASSWD • host key obrigatória • segredos nunca são logados\n")
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewForm(title, description string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	if description != "" {
		b.WriteString(description + "\n\n")
	}
	for i, item := range m.form {
		prefix := "  "
		if i == m.focus {
			prefix = "> "
		}
		if item.toggle != nil {
			mark := "[ ]"
			if *item.toggle {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "%s%-28s %s\n", prefix, item.label, mark)
			continue
		}
		if item.choice != nil {
			value := *item.choice
			if item.choiceDisplay != nil {
				value = item.choiceDisplay(value)
			}
			fmt.Fprintf(&b, "%s%-28s [%s] (espaço alterna)\n", prefix, item.label, value)
			continue
		}
		if item.input != nil {
			fmt.Fprintf(&b, "%s%-28s %s\n", prefix, item.label, item.input.View())
		}
	}
	if m.screen == screenConfig {
		fmt.Fprintf(&b, "\nRepositórios adicionais: %d\n", len(m.repoDrafts))
		for i, draft := range m.repoDrafts {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, draft.summary())
		}
		if m.repoFamilyMismatch() {
			b.WriteString("  " + errorStyle.Render("há repositórios de outra família de destino") + "\n")
		}
		b.WriteString("  ctrl+r gerenciar repositórios\n")
	}
	if m.screen == screenSSH {
		fmt.Fprintf(&b, "\nPontes SSH: %d\n", len(m.jumpDrafts))
		if len(m.jumpDrafts) == 0 {
			b.WriteString("  nenhuma — conexão direta ao destino\n")
		} else {
			for i, draft := range m.jumpDrafts {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, draft.summary())
			}
		}
		b.WriteString("  ctrl+j gerenciar pontes\n")
	}
	if m.screen == screenPackages && !m.sshEnabled {
		b.WriteString("\nModo apenas baixar: instalação e SSH estão desativados.\n")
	}
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewProgress() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Executando pipeline") + "\n\n")
	if m.progressText != "" {
		b.WriteString(m.progressText + "\n")
	}
	width := 30
	filled := int(m.progress * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	b.WriteString("[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]\n\n")
	for i := 0; i < core.TotalEtapas; i++ {
		fmt.Fprintf(&b, "%s %02d. %s\n", m.steps[i], i+1, m.stepName[i])
	}
	b.WriteString("\nLog (últimas linhas):\n")
	start := 0
	if len(m.logs) > 25 {
		start = len(m.logs) - 25
	}
	for _, line := range m.logs[start:] {
		b.WriteString("  " + line + "\n")
	}
	if m.confirm != nil {
		b.WriteString("\n" + selectedStyle.Render(fmt.Sprintf("Instalar %d artefatos? [y/n]", m.confirm.count)) + "\n")
	}
	return b.String()
}

func (m *Model) viewResult() string {
	var b strings.Builder
	if m.runErr != nil {
		b.WriteString(errorStyle.Render("Sessão encerrada com erro") + "\n\n")
		b.WriteString(m.runErr.Error() + "\n")
	} else if m.result != nil {
		title := "Sessão concluída"
		if m.result.InstallDeclined {
			title = "Transferência concluída — instalação recusada"
		}
		if m.result.AllAlreadyInstalled {
			title = "Todos os pacotes já estavam instalados"
		}
		if m.result.Installed {
			title = "Instalação concluída"
		}
		b.WriteString(titleStyle.Render(title) + "\n\n")
		fmt.Fprintf(&b, "Sessão: #%d\nArtefatos: %d\nSaída: %s\nManifesto: %s\nDestino remoto: %s\n", m.result.SessaoID, len(m.result.Artifacts), m.result.OutputDir, m.result.CaminhoManifesto, m.result.RemoteDir)
		if len(m.result.AlreadyInstalled) > 0 {
			var names []string
			for _, p := range m.result.AlreadyInstalled {
				names = append(names, p.Name)
			}
			b.WriteString("Já instalados: " + strings.Join(names, ", ") + "\n")
		}
	} else {
		b.WriteString("Sem resultado.\n")
	}
	b.WriteString("\n[r] relatório  [h] histórico  [n] nova sessão  [esc] início\n")
	return b.String()
}

func (m *Model) viewReport() string {
	text := m.reportText
	if m.errorMsg != "" {
		text += "\n\n" + m.errorMsg
	}
	lines := strings.Split(text, "\n")
	limit := 30
	if m.height > 10 {
		limit = m.height - 8
	}
	if m.reportOffset > len(lines) {
		m.reportOffset = len(lines)
	}
	end := m.reportOffset + limit
	if end > len(lines) {
		end = len(lines)
	}
	visible := strings.Join(lines[m.reportOffset:end], "\n")
	return titleStyle.Render("Relatório da sessão") + fmt.Sprintf(" (linhas %d-%d/%d)", m.reportOffset+1, end, len(lines)) + "\n\n" + visible
}

func (m *Model) viewHistory() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Histórico") + "\n\n")
	if len(m.sessions) == 0 {
		b.WriteString("Nenhuma sessão.\n")
		return b.String()
	}
	for i, sessao := range m.sessions {
		prefix := "  "
		line := fmt.Sprintf("#%d  %s %s  %s  %s", sessao.ID, sessao.TargetOS, sessao.TargetVersion, sessao.Status, sessao.HostDestino)
		if sessao.Arquivada {
			line += " (arquivada)"
		}
		if i == m.sessionIdx {
			prefix = "> "
			line = selectedStyle.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n[enter] abrir  [r] retomar  [a] arquivar  [esc] voltar\n")
	return b.String()
}

func (m *Model) viewRepos() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Repositórios adicionais") + "\n\n")
	b.WriteString("Usados somente no ambiente isolado de download; a lista é aditiva.\n\n")
	if len(m.repoDrafts) == 0 {
		b.WriteString("Nenhum repositório adicional; o download usará apenas os oficiais.\n")
	} else {
		for i, draft := range m.repoDrafts {
			prefix := "  "
			line := fmt.Sprintf("%d. %s", i+1, draft.summary())
			if i == m.repoIdx {
				prefix = "> "
				line = selectedStyle.Render(line)
			}
			b.WriteString(prefix + line + "\n")
		}
	}
	if m.repoFamilyMismatch() {
		b.WriteString("\n" + errorStyle.Render("Há repositórios de outra família de destino; remova-os ou ajuste o alvo.") + "\n")
	}
	b.WriteString("\n[a] adicionar  [e/enter] editar  [d] remover  [esc] voltar\n")
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewJumps() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Pontes SSH") + "\n\n")
	if len(m.jumpDrafts) == 0 {
		b.WriteString("Nenhuma ponte configurada; a conexão vai direto ao destino.\n")
	} else {
		for i, draft := range m.jumpDrafts {
			prefix := "  "
			line := fmt.Sprintf("%d. %s", i+1, draft.summary())
			if i == m.jumpIdx {
				prefix = "> "
				line = selectedStyle.Render(line)
			}
			b.WriteString(prefix + line + "\n")
		}
	}
	b.WriteString("\n[a] adicionar  [e/enter] editar  [d] remover  [K]/[J] mover  [esc] voltar\n")
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Configurações Globais") + "\n\n")
	b.WriteString("Arquivo: " + m.settingsPath + "\n\n")
	b.WriteString(m.viewForm("", "Configurações não sensíveis; senhas nunca são gravadas."))
	b.WriteString("\nSyslog: RFC 5424 assíncrono (UDP/TCP); falhas não interrompem o pipeline.\n")
	return b.String()
}

func (m *Model) helpText() string {
	switch m.screen {
	case screenWelcome:
		return "↑/↓ navegar • enter selecionar • q sair"
	case screenConfig:
		return "tab/↑↓ campos • ctrl+r repositórios • enter próximo • esc voltar • ctrl+c sair"
	case screenRepos:
		return "↑/↓ navegar • a adicionar • e/enter editar • d remover • esc voltar"
	case screenRepoEdit:
		return "tab/↑↓ campos • espaço alternar • enter salvar • esc descartar"
	case screenSSH:
		return "tab/↑↓ campos • ctrl+j pontes • ctrl+d detectar • ctrl+t testar • ctrl+a avançado • enter detectar/continuar • esc voltar"
	case screenJumps:
		return "↑/↓ navegar • a adicionar • e/enter editar • d remover • K/J mover • esc voltar"
	case screenJumpEdit:
		return "tab/↑↓ campos • espaço alternar autenticação • enter salvar • esc descartar"
	case screenPackages:
		return "tab/↑↓ campos • espaço alternar • enter iniciar • esc voltar"
	case screenProgress:
		return "c cancelar • y/n confirmar instalação • ctrl+c sair"
	case screenResult:
		return "r relatório • h histórico • n nova • esc início"
	case screenReport:
		return "↑/↓ rolar • pgup/pgdown • J JSON • M Markdown • S Syslog • esc voltar"
	case screenHistory:
		return "↑↓ navegar • enter abrir • r retomar • a arquivar • esc voltar"
	case screenSettings:
		return "tab/↑↓ campos • ctrl+s salvar • esc voltar"
	default:
		return ""
	}
}

var _ tea.Model = (*Model)(nil)
