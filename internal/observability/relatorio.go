package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opf/internal/core"
)

// SendRelatorioSyslog envia o relatório como um evento estruturado
// best-effort ao Syslog configurado.
func SendRelatorioSyslog(ctx context.Context, cfg SyslogConfig, relatorio *Relatorio) error {
	sink, err := NewSyslogSink(cfg)
	if err != nil {
		return err
	}
	if sink == nil {
		return fmt.Errorf("syslog não habilitado")
	}
	defer sink.Close(context.Background())
	data, err := relatorio.JSON()
	if err != nil {
		return err
	}
	return sink.Emit(ctx, core.Evento{
		Timestamp: time.Now().UTC(),
		Tipo:      "relatorio",
		Mensagem:  fmt.Sprintf("relatório da sessão #%d", relatorio.Sessao.ID),
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
	LocalDestino  string                  `json:"local_destino"`
	RemoteDestino string                  `json:"remote_destino"`
	GeradoEm      time.Time               `json:"gerado_em"`
}

func GerarRelatorio(ctx context.Context, repo core.Repository, sessaoID int64) (*Relatorio, error) {
	sessao, err := repo.ObterSessao(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("obter sessão do relatório: %w", err)
	}
	pacotes, err := repo.ListarPacotes(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar pacotes do relatório: %w", err)
	}
	eventos, err := repo.ListarEventos(ctx, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar eventos do relatório: %w", err)
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
	return &Relatorio{
		Sessao: *sessao, HostInfo: hostInfo, Solicitados: solicitados,
		Pacotes: relatorioPacotes, Eventos: eventos,
		LocalDestino: sessao.OutputDir, RemoteDestino: sessao.RemoteDir,
		GeradoEm: time.Now().UTC(),
	}, nil
}

func (r *Relatorio) JSON() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("relatório nulo")
	}
	return json.MarshalIndent(r, "", "  ")
}

func (r *Relatorio) Markdown() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Relatório da Sessão #%d\n\n", r.Sessao.ID)
	fmt.Fprintf(&b, "Gerado em: %s\n\n", r.GeradoEm.UTC().Format(time.RFC3339))

	b.WriteString("## Host remoto\n\n")
	b.WriteString("| Campo | Valor |\n|---|---|\n")
	hostRows := [][2]string{
		{"Host", r.Sessao.HostDestino},
		{"Usuário SSH", r.HostInfo.Usuario},
		{"Kernel", r.HostInfo.Kernel},
		{"Uptime", r.HostInfo.Uptime},
		{"CPU", r.HostInfo.CPU},
		{"Memória", r.HostInfo.Memoria},
		{"Disco", r.HostInfo.Disco},
	}
	for _, row := range hostRows {
		fmt.Fprintf(&b, "| %s | %s |\n", row[0], escapeMarkdown(row[1]))
	}

	b.WriteString("\n## Destino e armazenamento\n\n")
	b.WriteString("| Campo | Valor |\n|---|---|\n")
	fmt.Fprintf(&b, "| SO | %s |\n", escapeMarkdown(r.Sessao.TargetOS))
	fmt.Fprintf(&b, "| Versão | %s |\n", escapeMarkdown(r.Sessao.TargetVersion))
	fmt.Fprintf(&b, "| Arquitetura | %s |\n", escapeMarkdown(r.Sessao.TargetArch))
	fmt.Fprintf(&b, "| Método | %s |\n", escapeMarkdown(r.Sessao.FetchMethod))
	fmt.Fprintf(&b, "| Status | %s |\n", escapeMarkdown(string(r.Sessao.Status)))
	fmt.Fprintf(&b, "| Início | %s |\n", r.Sessao.IniciadaEm.UTC().Format(time.RFC3339))
	if r.Sessao.FinalizadaEm != nil {
		fmt.Fprintf(&b, "| Fim | %s |\n", r.Sessao.FinalizadaEm.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "| Arquivos locais | %s |\n", escapeMarkdown(r.LocalDestino))
	fmt.Fprintf(&b, "| Arquivos remotos | %s |\n", escapeMarkdown(r.RemoteDestino))

	if len(r.Solicitados) > 0 {
		b.WriteString("\n## Pacotes solicitados\n\n")
		for _, p := range r.Solicitados {
			fmt.Fprintf(&b, "- %s (principal)\n", escapeMarkdown(p.Nome))
		}
	}

	b.WriteString("\n## Pacotes\n\n")
	b.WriteString("| Nome | Papel | Versão | Versão anterior | Arquitetura | Status | SHA-256 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, p := range r.Pacotes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdown(p.Nome), escapeMarkdown(p.Papel), escapeMarkdown(p.Versao),
			escapeMarkdown(p.VersaoAnterior), escapeMarkdown(p.Arquitetura),
			escapeMarkdown(string(p.Status)), escapeMarkdown(p.SHA256))
	}

	b.WriteString("\n## Eventos\n\n")
	b.WriteString("| Timestamp | Tipo | Mensagem |\n|---|---|---|\n")
	for _, e := range r.Eventos {
		fmt.Fprintf(&b, "| %s | %s | %s |\n",
			e.Timestamp.UTC().Format(time.RFC3339), escapeMarkdown(e.Tipo), escapeMarkdown(e.Mensagem))
	}
	b.WriteString("\n> Exportação gerada localmente. Nenhum segredo é incluído.\n")
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
		return fmt.Errorf("caminho do relatório não informado")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		if filepath.Dir(path) == "." {
			return err
		}
		return fmt.Errorf("criar diretório do relatório: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("escrever relatório %q: %w", path, err)
	}
	return nil
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
