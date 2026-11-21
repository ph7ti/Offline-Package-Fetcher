package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"opf/internal/core"
	"opf/internal/i18n"
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
		body = m.viewForm(i18n.T("Configuração da Sessão"), i18n.T("Presets validados: ubuntu 22.04/24.04, debian 12, centos 7 (imagem preparada) e alpine 3.20. O método auto usa WSL compatível no Windows e Docker nos demais casos."))
	case screenRepos:
		body = m.viewRepos()
	case screenRepoEdit:
		body = m.viewForm(i18n.T("Repositório adicional"), i18n.T("Usado somente no ambiente isolado de download; a lista é aditiva e não substitui os repositórios oficiais. APK aceita CA customizada, mas não insecure TLS."))
	case screenSSH:
		body = m.viewForm(i18n.T("Conexão SSH e Jump Hosts"), i18n.T("Conecte primeiro; Enter/ctrl+d detecta /etc/os-release e uname. Host key é estrita por padrão; o toggle “Ignorar” equivale a StrictHostKeyChecking=no e é inseguro."))
		if m.detectedText != "" && m.detectedMatchesSSH() {
			body += "\n" + i18n.T("Destino detectado: %s", m.detectedText) + "\n"
		} else if m.sshEnabled && !m.manualTarget {
			body += "\n" + i18n.T("Destino ainda não detectado ou configuração alterada: Enter/ctrl+d executa a detecção.") + "\n"
		}
	case screenJumps:
		body = m.viewJumps()
	case screenJumpEdit:
		body = m.viewForm(i18n.T("Ponte SSH"), i18n.T("Cada ponte usa credenciais próprias; senhas ficam apenas em memória e nunca são persistidas. Vazio em known_hosts herda o valor do destino."))
	case screenPackages:
		body = m.viewForm(i18n.T("Pacotes e Pré-check"), i18n.T("Pré-check remoto automático; use Forçar reinstalação para ignorar pacotes presentes. A instalação nunca é retomada automaticamente."))
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
	header := titleStyle.Render("OPF TUI 2.3") + "  " + helpStyle.Render("terminal • CGO_ENABLED=0")
	help := helpStyle.Render(m.helpText())
	return header + "\n" + strings.Repeat("─", 72) + "\n\n" + body + "\n\n" + help
}

func (m *Model) viewWelcome() string {
	options := []string{
		i18n.T("Nova sessão"),
		i18n.T("Continuar sessão"),
		i18n.T("Histórico"),
		i18n.T("Configurações globais"),
		i18n.T("Sair"),
	}
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
		b.WriteString(i18n.T("SQLite indisponível") + "\n")
	} else {
		b.WriteString(i18n.T("SQLite: OK") + "\n")
	}
	b.WriteString(i18n.T("Sem NOPASSWD • host key obrigatória • segredos nunca são logados") + "\n")
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
			fmt.Fprintf(&b, "%s%-28s [%s] (%s)\n", prefix, item.label, value, i18n.T("espaço alterna"))
			continue
		}
		if item.input != nil {
			fmt.Fprintf(&b, "%s%-28s %s\n", prefix, item.label, item.input.View())
		}
	}
	if m.screen == screenConfig {
		fmt.Fprintf(&b, "\n%s\n", i18n.T("Repositórios adicionais: %d", len(m.repoDrafts)))
		for i, draft := range m.repoDrafts {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, draft.summary())
		}
		if m.repoFamilyMismatch() {
			b.WriteString("  " + errorStyle.Render(i18n.T("há repositórios de outra família de destino")) + "\n")
		}
		b.WriteString("  " + i18n.T("ctrl+r gerenciar repositórios") + "\n")
	}
	if m.screen == screenSSH {
		fmt.Fprintf(&b, "\n%s\n", i18n.T("Pontes SSH: %d", len(m.jumpDrafts)))
		if len(m.jumpDrafts) == 0 {
			b.WriteString("  " + i18n.T("nenhuma — conexão direta ao destino") + "\n")
		} else {
			for i, draft := range m.jumpDrafts {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, draft.summary())
			}
		}
		b.WriteString("  " + i18n.T("ctrl+j gerenciar pontes") + "\n")
	}
	if m.screen == screenPackages && !m.sshEnabled {
		b.WriteString("\n" + i18n.T("Modo apenas baixar: instalação e SSH estão desativados.") + "\n")
	}
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewProgress() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Executando pipeline")) + "\n\n")
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
	b.WriteString("\n" + i18n.T("Log (últimas linhas):") + "\n")
	start := 0
	if len(m.logs) > 25 {
		start = len(m.logs) - 25
	}
	for _, line := range m.logs[start:] {
		b.WriteString("  " + line + "\n")
	}
	if m.confirm != nil {
		b.WriteString("\n" + selectedStyle.Render(i18n.T("Instalar %d artefatos? [y/n]", m.confirm.count)) + "\n")
	}
	return b.String()
}

func (m *Model) viewResult() string {
	var b strings.Builder
	if m.runErr != nil {
		b.WriteString(errorStyle.Render(i18n.T("Sessão encerrada com erro")) + "\n\n")
		b.WriteString(m.runErr.Error() + "\n")
	} else if m.result != nil {
		title := i18n.T("Sessão concluída")
		if m.result.InstallDeclined {
			title = i18n.T("Transferência concluída — instalação recusada")
		}
		if m.result.AllAlreadyInstalled {
			title = i18n.T("Todos os pacotes já estavam instalados")
		}
		if m.result.Installed {
			title = i18n.T("Instalação concluída")
		}
		b.WriteString(titleStyle.Render(title) + "\n\n")
		fmt.Fprintf(&b, "%s #%d\n%s %d\n%s %s\n%s %s\n%s %s\n",
			i18n.T("Sessão:"), m.result.SessaoID,
			i18n.T("Artefatos:"), len(m.result.Artifacts),
			i18n.T("Saída:"), m.result.OutputDir,
			i18n.T("Manifesto:"), m.result.CaminhoManifesto,
			i18n.T("Destino remoto:"), m.result.RemoteDir)
		if len(m.result.AlreadyInstalled) > 0 {
			var names []string
			for _, p := range m.result.AlreadyInstalled {
				names = append(names, p.Name)
			}
			b.WriteString(i18n.T("Já instalados: %s", strings.Join(names, ", ")) + "\n")
		}
	} else {
		b.WriteString(i18n.T("Sem resultado.") + "\n")
	}
	b.WriteString("\n" + i18n.T("[r] relatório  [h] histórico  [n] nova sessão  [esc] início") + "\n")
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
	return titleStyle.Render(i18n.T("Relatório da sessão")) + " " + i18n.T("(linhas %d-%d/%d)", m.reportOffset+1, end, len(lines)) + "\n\n" + visible
}

func (m *Model) viewHistory() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Histórico")) + "\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(i18n.T("Nenhuma sessão.") + "\n")
		return b.String()
	}
	for i, sessao := range m.sessions {
		prefix := "  "
		line := fmt.Sprintf("#%d  %s %s  %s  %s", sessao.ID, sessao.TargetOS, sessao.TargetVersion, sessao.Status, sessao.HostDestino)
		if sessao.Arquivada {
			line += " " + i18n.T("(arquivada)")
		}
		if i == m.sessionIdx {
			prefix = "> "
			line = selectedStyle.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	b.WriteString("\n" + i18n.T("[enter] abrir  [r] retomar  [a] arquivar  [esc] voltar") + "\n")
	return b.String()
}

func (m *Model) viewRepos() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Repositórios adicionais")) + "\n\n")
	b.WriteString(i18n.T("Usados somente no ambiente isolado de download; a lista é aditiva.") + "\n\n")
	if len(m.repoDrafts) == 0 {
		b.WriteString(i18n.T("Nenhum repositório adicional; o download usará apenas os oficiais.") + "\n")
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
		b.WriteString("\n" + errorStyle.Render(i18n.T("Há repositórios de outra família de destino; remova-os ou ajuste o alvo.")) + "\n")
	}
	b.WriteString("\n" + i18n.T("[a] adicionar  [e/enter] editar  [d] remover  [esc] voltar") + "\n")
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewJumps() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Pontes SSH")) + "\n\n")
	if len(m.jumpDrafts) == 0 {
		b.WriteString(i18n.T("Nenhuma ponte configurada; a conexão vai direto ao destino.") + "\n")
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
	b.WriteString("\n" + i18n.T("[a] adicionar  [e/enter] editar  [d] remover  [K]/[J] mover  [esc] voltar") + "\n")
	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render(m.errorMsg) + "\n")
	}
	return b.String()
}

func (m *Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Configurações Globais")) + "\n\n")
	b.WriteString(i18n.T("Arquivo: %s", m.settingsPath) + "\n")
	b.WriteString(i18n.T("Idioma atual: %s (%s)", languageSettingValue(m.settings.Language), i18n.NativeName(i18n.Current())) + "\n\n")
	b.WriteString(m.viewForm("", i18n.T("Configurações não sensíveis; senhas nunca são gravadas.")))
	b.WriteString(i18n.T("Valores aceitos: auto, en, pt-BR, es") + "\n")
	b.WriteString("\n" + i18n.T("Syslog: RFC 5424 assíncrono (UDP/TCP); falhas não interrompem o pipeline.") + "\n")
	return b.String()
}

func (m *Model) helpText() string {
	switch m.screen {
	case screenWelcome:
		return i18n.T("↑/↓ navegar • enter selecionar • q sair")
	case screenConfig:
		return i18n.T("tab/↑↓ campos • ctrl+r repositórios • enter próximo • esc voltar • ctrl+c sair")
	case screenRepos:
		return i18n.T("↑/↓ navegar • a adicionar • e/enter editar • d remover • esc voltar")
	case screenRepoEdit:
		return i18n.T("tab/↑↓ campos • espaço alternar • enter salvar • esc descartar")
	case screenSSH:
		return i18n.T("tab/↑↓ campos • ctrl+j pontes • ctrl+d detectar • ctrl+t testar • ctrl+a avançado • enter detectar/continuar • esc voltar")
	case screenJumps:
		return i18n.T("↑/↓ navegar • a adicionar • e/enter editar • d remover • K/J mover • esc voltar")
	case screenJumpEdit:
		return i18n.T("tab/↑↓ campos • espaço alternar autenticação • enter salvar • esc descartar")
	case screenPackages:
		return i18n.T("tab/↑↓ campos • espaço alternar • enter iniciar • esc voltar")
	case screenProgress:
		return i18n.T("c cancelar • y/n confirmar instalação • ctrl+c sair")
	case screenResult:
		return i18n.T("r relatório • h histórico • n nova • esc início")
	case screenReport:
		return i18n.T("↑/↓ rolar • pgup/pgdown • J JSON • M Markdown • S Syslog • esc voltar")
	case screenHistory:
		return i18n.T("↑↓ navegar • enter abrir • r retomar • a arquivar • esc voltar")
	case screenSettings:
		return i18n.T("tab/↑↓ campos • ctrl+s salvar • esc voltar")
	default:
		return ""
	}
}

var _ tea.Model = (*Model)(nil)
