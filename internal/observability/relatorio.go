package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opf/internal/core"
	"opf/internal/i18n"
)

// SendRelatorioSyslog envia o relatório como um evento estruturado
// best-effort ao Syslog configurado.
func SendRelatorioSyslog(ctx context.Context, cfg SyslogConfig, relatorio *Relatorio) error {
	sink, err := NewSyslogSink(cfg)
	if err != nil {
		return err
	}
	if sink == nil {
		return errors.New(i18n.T("syslog não habilitado"))
	}
	defer sink.Close(context.Background())
	data, err := relatorio.JSON()
	if err != nil {
		return err
	}
	return sink.Emit(ctx, core.Evento{
		Timestamp: time.Now().UTC(),
		Tipo:      "relatorio",
		Mensagem:  i18n.T("relatório da sessão #%d", relatorio.Sessao.ID),
		Detalhes:  string(data),
	})
}

type RelatorioPacote struct {
	core.Pacote
	Papel string `json:"papel"` // principal | dependente
}

type Relatorio struct {
	Sessao        core.Sessao             `json:"sessao"`
	HostInfo      core.HostInfo           `json:"host_info"`
	Solicitados   []core.PacoteSolicitado `json:"pacotes_solicitados"`
	Pacotes       []RelatorioPacote       `json:"pacotes"`
	Eventos       []core.Evento           `json:"eventos"`
	Preview       core.PackagePreviewData `json:"preview"`
	LocalDestino  string                  `json:"local_destino"`
	RemoteDestino string                  `json:"remote_destino"`
	GeradoEm      time.Time               `json:"gerado_em"`
}

func GerarRelatorio(ctx context.Context, repo core.Repository, sessaoID int64) (*Relatorio, error) {
	sessao, err := repo.ObterSessao(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("obter sessão do relatório: %w"), err)
	}
	pacotes, err := repo.ListarPacotes(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("listar pacotes do relatório: %w"), err)
	}
	eventos, err := repo.ListarEventos(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("listar eventos do relatório: %w"), err)
	}
	relatorioPacotes := make([]RelatorioPacote, 0, len(pacotes))
	for _, pacote := range pacotes {
		papel := "principal"
		if pacote.EhDependencia {
			papel = "dependente"
		}
		relatorioPacotes = append(relatorioPacotes, RelatorioPacote{Pacote: pacote, Papel: papel})
	}
	var hostInfo core.HostInfo
	if sessao.HostInfoJSON != "" {
		_ = json.Unmarshal([]byte(sessao.HostInfoJSON), &hostInfo)
	}
	var solicitados []core.PacoteSolicitado
	if repo, ok := repo.(interface {
		ListarPacotesSolicitados(context.Context, int64) ([]core.PacoteSolicitado, error)
	}); ok {
		solicitados, _ = repo.ListarPacotesSolicitados(ctx, sessaoID)
	}
	var preview core.PackagePreviewData
	if sessao.PreviewJSON != "" {
		_ = json.Unmarshal([]byte(sessao.PreviewJSON), &preview)
	}
	return &Relatorio{
		Sessao: *sessao, HostInfo: hostInfo, Solicitados: solicitados,
		Pacotes: relatorioPacotes, Eventos: eventos, Preview: preview,
		LocalDestino: sessao.OutputDir, RemoteDestino: sessao.RemoteDir,
		GeradoEm: time.Now().UTC(),
	}, nil
}

func (r *Relatorio) JSON() ([]byte, error) {
	if r == nil {
		return nil, errors.New(i18n.T("relatório nulo"))
	}
	return json.MarshalIndent(r, "", "  ")
}

func (r *Relatorio) Markdown() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, i18n.T("# Relatório da Sessão #%d")+"\n\n", r.Sessao.ID)
	fmt.Fprintf(&b, i18n.T("Gerado em: %s")+"\n\n", r.GeradoEm.UTC().Format(time.RFC3339))

	b.WriteString(i18n.T("## Host remoto") + "\n\n")
	b.WriteString(i18n.T("| Campo | Valor |") + "\n|---|---|\n")
	hostRows := [][2]string{
		{i18n.T("Host"), r.Sessao.HostDestino},
		{i18n.T("Usuário SSH"), r.HostInfo.Usuario},
		{i18n.T("Kernel"), r.HostInfo.Kernel},
		{i18n.T("Uptime"), r.HostInfo.Uptime},
		{i18n.T("CPU"), r.HostInfo.CPU},
		{i18n.T("Memória"), r.HostInfo.Memoria},
		{i18n.T("Disco"), r.HostInfo.Disco},
	}
	for _, row := range hostRows {
		fmt.Fprintf(&b, "| %s | %s |\n", row[0], escapeMarkdown(row[1]))
	}

	b.WriteString("\n" + i18n.T("## Destino e armazenamento") + "\n\n")
	b.WriteString(i18n.T("| Campo | Valor |") + "\n|---|---|\n")
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("SO"), escapeMarkdown(r.Sessao.TargetOS))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Versão"), escapeMarkdown(r.Sessao.TargetVersion))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Arquitetura"), escapeMarkdown(r.Sessao.TargetArch))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Método"), escapeMarkdown(r.Sessao.FetchMethod))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Status"), escapeMarkdown(string(r.Sessao.Status)))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Início"), r.Sessao.IniciadaEm.UTC().Format(time.RFC3339))
	if r.Sessao.FinalizadaEm != nil {
		fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Fim"), r.Sessao.FinalizadaEm.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Arquivos locais"), escapeMarkdown(r.LocalDestino))
	fmt.Fprintf(&b, "| %s | %s |\n", i18n.T("Arquivos remotos"), escapeMarkdown(r.RemoteDestino))

	if len(r.Solicitados) > 0 {
		b.WriteString("\n" + i18n.T("## Pacotes solicitados") + "\n\n")
		for _, p := range r.Solicitados {
			fmt.Fprintf(&b, i18n.T("- %s (principal)")+"\n", escapeMarkdown(p.Nome))
		}
	}

	// Only the human-readable labels follow the interface language; the JSON
	// payload keeps its stable keys and raw values (role and status codes).
	b.WriteString("\n" + i18n.T("## Pacotes") + "\n\n")
	b.WriteString(i18n.T("| Nome | Papel | Versão | Versão anterior | Arquitetura | Status | SHA-256 |") + "\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, p := range r.Pacotes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdown(p.Nome), escapeMarkdown(p.Papel), escapeMarkdown(p.Versao),
			escapeMarkdown(p.VersaoAnterior), escapeMarkdown(p.Arquitetura),
			escapeMarkdown(string(p.Status)), escapeMarkdown(p.SHA256))
	}

	if len(r.Preview.Inventory) > 0 {
		b.WriteString("\n" + i18n.T("## Pacotes instalados no destino") + "\n\n")
		b.WriteString(i18n.T("| Pacote | Versão | Arquitetura |") + "\n|---|---|---|\n")
		for _, pkg := range r.Preview.Inventory {
			fmt.Fprintf(&b, "| %s | %s | %s |\n",
				escapeMarkdown(pkg.Name), escapeMarkdown(pkg.Version), escapeMarkdown(pkg.Arch))
		}
	}
	if r.Preview.Mode != "" {
		b.WriteString("\n" + i18n.T("## Package diff") + "\n\n")
		if len(r.Preview.Diff) == 0 {
			b.WriteString(i18n.T("- Nenhuma atualização de pacote foi detectada.") + "\n")
		} else {
			b.WriteString(i18n.T("| Pacote | De | Para | Salto | Papel |") + "\n|---|---|---|---|---|\n")
			for _, diff := range r.Preview.Diff {
				role := i18n.T("principal")
				if diff.IsDependency {
					role = i18n.T("dependência")
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s → %s | %s |\n",
					escapeMarkdown(diff.Name), escapeMarkdown(diff.From), escapeMarkdown(diff.To),
					escapeMarkdown(diff.From), escapeMarkdown(diff.To), escapeMarkdown(role))
			}
			b.WriteString("\n")
			for _, diff := range r.Preview.Diff {
				fmt.Fprintf(&b, i18n.T("- De pacote %s versão %s para versão %s (salto %s → %s).")+"\n",
					escapeMarkdown(diff.Name), escapeMarkdown(diff.From), escapeMarkdown(diff.To),
					escapeMarkdown(diff.From), escapeMarkdown(diff.To))
			}
		}
		if r.Preview.Decision != "" {
			decision := i18n.T("aprovado")
			if r.Preview.Decision == "aborted" {
				decision = i18n.T("recusado — sessão abortada")
			}
			fmt.Fprintf(&b, "\n%s: **%s**\n", i18n.T("Decisão do usuário"), decision)
		}
	}

	b.WriteString("\n" + i18n.T("## Eventos") + "\n\n")
	b.WriteString(i18n.T("| Timestamp | Tipo | Mensagem |") + "\n|---|---|---|\n")
	for _, e := range r.Eventos {
		fmt.Fprintf(&b, "| %s | %s | %s |\n",
			e.Timestamp.UTC().Format(time.RFC3339), escapeMarkdown(e.Tipo), escapeMarkdown(e.Mensagem))
	}
	b.WriteString("\n" + i18n.T("> Exportação gerada localmente. Nenhum segredo é incluído.") + "\n")
	return b.String()
}

func SalvarJSON(path string, relatorio *Relatorio) error {
	data, err := relatorio.JSON()
	if err != nil {
		return err
	}
	return writeReport(path, append(data, '\n'))
}

func SalvarMarkdown(path string, relatorio *Relatorio) error {
	return writeReport(path, []byte(relatorio.Markdown()))
}

func writeReport(path string, data []byte) error {
	if path == "" {
		return errors.New(i18n.T("caminho do relatório não informado"))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		if filepath.Dir(path) == "." {
			return err
		}
		return fmt.Errorf(i18n.T("criar diretório do relatório: %w"), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf(i18n.T("escrever relatório %q: %w"), path, err)
	}
	return nil
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
