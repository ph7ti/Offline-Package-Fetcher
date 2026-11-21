package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opf/internal/i18n"
)

type SessaoStatus string

const (
	SessaoEmAndamento SessaoStatus = "em_andamento"
	SessaoConcluida   SessaoStatus = "concluida"
	SessaoFalhou      SessaoStatus = "falhou"
	SessaoAbortada    SessaoStatus = "abortada"
)

type PacoteStatus string

const (
	PacoteBaixado             PacoteStatus = "baixado"
	PacoteEnviado             PacoteStatus = "enviado"
	PacoteEnviadoNaoInstalado PacoteStatus = "enviado_nao_instalado"
	PacoteInstalado           PacoteStatus = "instalado"
	PacoteFalhou              PacoteStatus = "falhou"
	PacoteJaInstalado         PacoteStatus = "ja_instalado"
)

const (
	EventoDownload      = "download"
	EventoTransferencia = "transferencia"
	EventoInstalacao    = "instalacao"
	EventoErro          = "erro"
	EventoInfo          = "info"
	EventoPreCheck      = "precheck"
)

type EtapaStatus string

const (
	EtapaPendente    EtapaStatus = "pendente"
	EtapaEmAndamento EtapaStatus = "em_andamento"
	EtapaConcluida   EtapaStatus = "concluida"
	EtapaFalhou      EtapaStatus = "falhou"
	EtapaPulada      EtapaStatus = "pulada"
)

type EtapaSessao struct {
	SessaoID     int64
	Etapa        int
	Nome         string
	Status       EtapaStatus
	IniciadaEm   *time.Time
	FinalizadaEm *time.Time
	Erro         string
}

// Sessao is the pipeline coordinator state and the persisted session record.
type Sessao struct {
	ID              int64
	IniciadaEm      time.Time
	FinalizadaEm    *time.Time
	TargetOS        string
	TargetVersion   string
	TargetArch      string
	FetchMethod     string
	HostDestino     string
	IncludeOptional bool
	Status          SessaoStatus

	EtapaAtual   int
	OutputDir    string
	RemoteDir    string
	ImagemDocker string
	WSLDistro    string
	AmbienteID   string
	ConfigJSON   string
	HostInfoJSON string
	Arquivada    bool
}

func (s *Sessao) Validate() error {
	if s == nil {
		return errors.New(i18n.T("sessao nula"))
	}
	if s.TargetOS == "" {
		return errors.New(i18n.T("SO de destino não informado"))
	}
	if s.TargetVersion == "" {
		return errors.New(i18n.T("versão de destino não informada"))
	}
	if s.TargetArch == "" {
		return errors.New(i18n.T("arquitetura de destino não informada"))
	}
	switch s.FetchMethod {
	case "docker", "wsl":
	default:
		return fmt.Errorf(i18n.T("método de fetch inválido: %q"), s.FetchMethod)
	}
	if s.Status == "" {
		s.Status = SessaoEmAndamento
	}
	return nil
}

// Pacote is a downloaded package record persisted in SQLite.
type Pacote struct {
	ID                  int64
	SessaoID            int64
	Nome                string
	Versao              string
	Arquitetura         string
	Status              PacoteStatus
	EhDependencia       bool
	CaminhoLocal        string
	SHA256              string
	SHA256P1            string
	SHA256P2            string
	VersaoAnterior      string
	P3Verificado        bool
	InstaladoVerificado bool
}

type PacoteSolicitado struct {
	ID       int64
	SessaoID int64
	Nome     string
	Status   string
	Motivo   string
}

type HostInfo struct {
	Host    string `json:"host"`
	Usuario string `json:"usuario"`
	Kernel  string `json:"kernel"`
	Uptime  string `json:"uptime"`
	CPU     string `json:"cpu"`
	Memoria string `json:"memoria"`
	Disco   string `json:"disco"`
}

type SessaoResumo struct {
	Sessao
	TotalPacotes      int
	TotalInstalados   int
	TotalDependencias int
}

// Evento is an observability record persisted in SQLite.
type Evento struct {
	ID        int64
	SessaoID  int64
	Timestamp time.Time
	Tipo      string
	Mensagem  string
	Detalhes  string
}

// Repository is the persistence contract consumed by the core pipeline.
type Repository interface {
	CreateSessao(ctx context.Context, s *Sessao) error
	ObterSessao(ctx context.Context, id int64) (*Sessao, error)
	FinalizarSessao(ctx context.Context, id int64, status SessaoStatus, at time.Time) error
	AdicionarPacote(ctx context.Context, p *Pacote) error
	AtualizarPacotesStatus(ctx context.Context, sessaoID int64, de, para PacoteStatus) error
	AdicionarEvento(ctx context.Context, e *Evento) error
	ListarPacotes(ctx context.Context, sessaoID int64) ([]Pacote, error)
	ListarEventos(ctx context.Context, sessaoID int64) ([]Evento, error)
}

// CheckpointRepository persists execution state for progress and resume.
type CheckpointRepository interface {
	ReabrirSessao(ctx context.Context, id int64) error
	AtualizarSessaoExecucao(ctx context.Context, id int64, outputDir, remoteDir, imagem, wslDistro, ambienteID, configJSON string) error
	AtualizarEtapaAtual(ctx context.Context, id int64, etapa int) error
	RegistrarEtapa(ctx context.Context, e *EtapaSessao) error
	ListarEtapas(ctx context.Context, sessaoID int64) ([]EtapaSessao, error)
	AdicionarPacoteSolicitado(ctx context.Context, p *PacoteSolicitado) error
	ListarPacotesSolicitados(ctx context.Context, sessaoID int64) ([]PacoteSolicitado, error)
	AtualizarPacoteVerificacoes(ctx context.Context, pacoteID int64, shaP1, shaP2 string, p3, instalado *bool) error
	AtualizarPacoteVersaoAnterior(ctx context.Context, pacoteID int64, versao string) error
	AtualizarHostInfo(ctx context.Context, sessaoID int64, infoJSON string) error
}

// HistoryRepository lists and archives sessions.
type HistoryRepository interface {
	ListarSessoes(ctx context.Context, somenteNaoArquivadas bool) ([]SessaoResumo, error)
	ArquivarSessao(ctx context.Context, id int64, arquivada bool) error
}

var nomesEtapas = map[int]string{
	1:  "abrir sessão",
	2:  "pré-check remoto",
	3:  "criar ambiente isolado",
	4:  "resolver dependências",
	5:  "baixar pacotes",
	6:  "copiar artefatos para o host",
	7:  "gerar e verificar manifesto",
	8:  "enviar via SFTP",
	9:  "verificar checksums no destino",
	10: "confirmar instalação",
	11: "instalar remotamente",
	12: "registrar resultado",
	13: "cleanup",
}

const TotalEtapas = 13

// NomeEtapa returns the human-readable name of a pipeline stage in the active
// interface language. The Portuguese source names stay in nomesEtapas; the
// translation happens at call time so a language change applies immediately.
func NomeEtapa(etapa int) string {
	if nome, ok := nomesEtapas[etapa]; ok {
		return i18n.T(nome)
	}
	return i18n.T("etapa %d", etapa)
}
