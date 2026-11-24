package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opf/internal/core"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opf.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func newTestSession() *core.Sessao {
	return &core.Sessao{
		TargetOS:        "ubuntu",
		TargetVersion:   "22.04",
		TargetArch:      "amd64",
		FetchMethod:     "docker",
		IncludeOptional: false,
	}
}

func TestCreateObterFinalizarSessao(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	sessao := newTestSession()

	if err := store.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	if sessao.ID <= 0 {
		t.Fatalf("id da sessão = %d", sessao.ID)
	}
	if sessao.IniciadaEm.IsZero() {
		t.Fatal("iniciada_em não preenchida")
	}

	stored, err := store.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.TargetOS != "ubuntu" || stored.TargetVersion != "22.04" || stored.TargetArch != "amd64" {
		t.Fatalf("dados da sessão divergentes: %+v", stored)
	}
	if stored.Status != core.SessaoEmAndamento {
		t.Fatalf("status = %q", stored.Status)
	}
	if stored.FinalizadaEm != nil {
		t.Fatal("finalizada_em deveria ser nula")
	}

	if err := store.FinalizarSessao(ctx, sessao.ID, core.SessaoConcluida, time.Now().UTC()); err != nil {
		t.Fatalf("FinalizarSessao: %v", err)
	}
	stored, err = store.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao final: %v", err)
	}
	if stored.Status != core.SessaoConcluida {
		t.Fatalf("status final = %q", stored.Status)
	}
	if stored.FinalizadaEm == nil {
		t.Fatal("finalizada_em deveria estar preenchida")
	}
}

func TestAdicionarListarPacotesEventos(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	sessao := newTestSession()
	if err := store.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}

	pacotes := []*core.Pacote{
		{
			SessaoID:      sessao.ID,
			Nome:          "vim",
			Versao:        "2:8.2.3995-1ubuntu2",
			Arquitetura:   "amd64",
			Status:        core.PacoteBaixado,
			EhDependencia: false,
			CaminhoLocal:  "/tmp/vim.deb",
			SHA256:        "abc",
		},
		{
			SessaoID:      sessao.ID,
			Nome:          "libc6",
			Versao:        "2.35-0ubuntu3",
			Arquitetura:   "amd64",
			Status:        core.PacoteBaixado,
			EhDependencia: true,
			CaminhoLocal:  "/tmp/libc6.deb",
			SHA256:        "def",
		},
	}
	for _, pacote := range pacotes {
		if err := store.AdicionarPacote(ctx, pacote); err != nil {
			t.Fatalf("AdicionarPacote: %v", err)
		}
	}

	evento := &core.Evento{
		SessaoID: sessao.ID,
		Tipo:     core.EventoDownload,
		Mensagem: "download concluído",
		Detalhes: `{"artefatos":2}`,
	}
	if err := store.AdicionarEvento(ctx, evento); err != nil {
		t.Fatalf("AdicionarEvento: %v", err)
	}

	listados, err := store.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(listados) != 2 {
		t.Fatalf("pacotes = %d", len(listados))
	}
	if listados[0].Nome != "libc6" || !listados[0].EhDependencia {
		t.Fatalf("ordem/flag de dependência incorretos: %+v", listados[0])
	}

	eventos, err := store.ListarEventos(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	if len(eventos) != 1 {
		t.Fatalf("eventos = %d", len(eventos))
	}
	if eventos[0].Tipo != core.EventoDownload || eventos[0].Timestamp.IsZero() {
		t.Fatalf("evento inválido: %+v", eventos[0])
	}

	if err := store.AtualizarPacotesStatus(ctx, sessao.ID, core.PacoteBaixado, core.PacoteEnviado); err != nil {
		t.Fatalf("AtualizarPacotesStatus: %v", err)
	}
	listados, err = store.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes após atualização: %v", err)
	}
	for _, pacote := range listados {
		if pacote.Status != core.PacoteEnviado {
			t.Fatalf("status após atualização = %q", pacote.Status)
		}
	}
	if err := store.AtualizarPacotesStatus(ctx, sessao.ID, "invalido", core.PacoteEnviado); err == nil {
		t.Fatal("esperava erro para status de origem inválido")
	}
	if err := store.AtualizarPacotesStatus(ctx, sessao.ID, core.PacoteBaixado, "invalido"); err == nil {
		t.Fatal("esperava erro para status de destino inválido")
	}
}

func TestCheckpointHistoricoEVerificacoes(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	sessao := newTestSession()
	if err := store.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}

	if err := store.AtualizarHostInfo(ctx, sessao.ID, `{"kernel":"6.8.0"}`); err != nil {
		t.Fatalf("AtualizarHostInfo: %v", err)
	}
	if err := store.AtualizarSessaoExecucao(ctx, sessao.ID, "/tmp/out", "/tmp/remote", "ubuntu:22.04", "", "container-1", `{"packages":["vim"]}`); err != nil {
		t.Fatalf("AtualizarSessaoExecucao: %v", err)
	}
	if err := store.RegistrarEtapa(ctx, &core.EtapaSessao{SessaoID: sessao.ID, Etapa: 1, Status: core.EtapaConcluida}); err != nil {
		t.Fatalf("RegistrarEtapa: %v", err)
	}
	if err := store.RegistrarEtapa(ctx, &core.EtapaSessao{SessaoID: sessao.ID, Etapa: 2, Status: core.EtapaEmAndamento}); err != nil {
		t.Fatalf("RegistrarEtapa 2: %v", err)
	}
	etapas, err := store.ListarEtapas(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarEtapas: %v", err)
	}
	if len(etapas) != 2 || etapas[0].Status != core.EtapaConcluida {
		t.Fatalf("etapas = %+v", etapas)
	}
	if err := store.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{SessaoID: sessao.ID, Nome: "vim"}); err != nil {
		t.Fatalf("AdicionarPacoteSolicitado: %v", err)
	}
	solicitados, err := store.ListarPacotesSolicitados(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotesSolicitados: %v", err)
	}
	if len(solicitados) != 1 || solicitados[0].Nome != "vim" {
		t.Fatalf("solicitados = %+v", solicitados)
	}

	pacote := &core.Pacote{SessaoID: sessao.ID, Nome: "vim", Versao: "9.0", Arquitetura: "amd64", Status: core.PacoteBaixado}
	if err := store.AdicionarPacote(ctx, pacote); err != nil {
		t.Fatalf("AdicionarPacote: %v", err)
	}
	yes := true
	if err := store.AtualizarPacoteVersaoAnterior(ctx, pacote.ID, "8.0"); err != nil {
		t.Fatalf("AtualizarPacoteVersaoAnterior: %v", err)
	}
	if err := store.AtualizarPacoteVerificacoes(ctx, pacote.ID, "p1", "p2", &yes, &yes); err != nil {
		t.Fatalf("AtualizarPacoteVerificacoes: %v", err)
	}
	pacotes, err := store.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if pacotes[0].SHA256P1 != "p1" || pacotes[0].VersaoAnterior != "8.0" || !pacotes[0].P3Verificado || !pacotes[0].InstaladoVerificado {
		t.Fatalf("verificações não persistidas: %+v", pacotes[0])
	}

	resumos, err := store.ListarSessoes(ctx, true)
	if err != nil {
		t.Fatalf("ListarSessoes: %v", err)
	}
	if len(resumos) != 1 || resumos[0].TotalPacotes != 1 {
		t.Fatalf("resumos = %+v", resumos)
	}
	if err := store.ArquivarSessao(ctx, sessao.ID, true); err != nil {
		t.Fatalf("ArquivarSessao: %v", err)
	}
	resumos, err = store.ListarSessoes(ctx, true)
	if err != nil {
		t.Fatalf("ListarSessoes arquivadas: %v", err)
	}
	if len(resumos) != 0 {
		t.Fatalf("sessão arquivada ainda listada: %+v", resumos)
	}
	if err := store.ReabrirSessao(ctx, sessao.ID); err != nil {
		t.Fatalf("ReabrirSessao: %v", err)
	}
	reaberta, err := store.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if reaberta.Status != core.SessaoEmAndamento || reaberta.FinalizadaEm != nil {
		t.Fatalf("sessão reaberta inválida: %+v", reaberta)
	}
	if reaberta.HostInfoJSON != `{"kernel":"6.8.0"}` {
		t.Fatalf("host info não persistida: %q", reaberta.HostInfoJSON)
	}
	if reaberta.OutputDir != "/tmp/out" || reaberta.RemoteDir != "/tmp/remote" || reaberta.AmbienteID != "container-1" {
		t.Fatalf("execução não persistida: %+v", reaberta)
	}
}

func TestPersistenciaAposReabrir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	ctx := context.Background()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sessao := newTestSession()
	if err := store.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer reopened.Close()

	stored, err := reopened.ObterSessao(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao após reabrir: %v", err)
	}
	if stored.TargetVersion != "22.04" {
		t.Fatalf("sessão persistida incorreta: %+v", stored)
	}
}

func TestMigracaoStatusEnviadoNaoInstalado(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("abrir SQLite antigo: %v", err)
	}
	oldSchema := sessoesTable + `
CREATE TABLE IF NOT EXISTS pacotes (
    id              INTEGER PRIMARY KEY,
    sessao_id       INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    nome            TEXT NOT NULL,
    versao          TEXT NOT NULL,
    arquitetura     TEXT,
    status          TEXT NOT NULL CHECK (status IN
                      ('baixado','enviado','instalado','falhou','ja_instalado')),
    eh_dependencia  INTEGER NOT NULL DEFAULT 0,
    caminho_local   TEXT,
    sha256          TEXT
);
CREATE INDEX IF NOT EXISTS idx_pacotes_sessao ON pacotes(sessao_id);
` + eventosTable
	if _, err := raw.Exec(oldSchema); err != nil {
		raw.Close()
		t.Fatalf("criar schema antigo: %v", err)
	}
	now := time.Now().UTC()
	if _, err := raw.Exec(`INSERT INTO sessoes (
		id, iniciada_em, so_destino, versao_destino, arch_destino,
		metodo_fetch, include_optional, status
	) VALUES (1, ?, 'ubuntu', '22.04', 'amd64', 'docker', 0, 'concluida')`,
		formatTime(now),
	); err != nil {
		raw.Close()
		t.Fatalf("inserir sessão antiga: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO pacotes (
		id, sessao_id, nome, versao, arquitetura, status,
		eh_dependencia, caminho_local, sha256
	) VALUES (1, 1, 'vim', '9.0', 'amd64', 'enviado', 0, '/tmp/vim.deb', 'abc')`,
	); err != nil {
		raw.Close()
		t.Fatalf("inserir pacote antigo: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("fechar SQLite antigo: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open com migração: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	pacotes, err := store.ListarPacotes(ctx, 1)
	if err != nil {
		t.Fatalf("ListarPacotes: %v", err)
	}
	if len(pacotes) != 1 || pacotes[0].Nome != "vim" {
		t.Fatalf("dados preservados incorretamente: %+v", pacotes)
	}
	if err := store.AtualizarPacotesStatus(ctx, 1, core.PacoteEnviado, core.PacoteEnviadoNaoInstalado); err != nil {
		t.Fatalf("novo status após migração: %v", err)
	}
	pacotes, err = store.ListarPacotes(ctx, 1)
	if err != nil {
		t.Fatalf("ListarPacotes após migração: %v", err)
	}
	if pacotes[0].Status != core.PacoteEnviadoNaoInstalado {
		t.Fatalf("status migrado = %q", pacotes[0].Status)
	}
}

func TestMigracaoFetchMethodPodman(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-fetch-method.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("abrir SQLite antigo: %v", err)
	}
	legacySessoes := `
CREATE TABLE IF NOT EXISTS sessoes (
    id              INTEGER PRIMARY KEY,
    iniciada_em     DATETIME NOT NULL,
    finalizada_em   DATETIME,
    so_destino      TEXT NOT NULL,
    versao_destino  TEXT NOT NULL,
    arch_destino    TEXT NOT NULL,
    metodo_fetch    TEXT NOT NULL CHECK (metodo_fetch IN ('docker','wsl')),
    include_optional INTEGER NOT NULL DEFAULT 0,
    host_destino    TEXT,
    status          TEXT NOT NULL CHECK (status IN ('em_andamento','concluida','falhou','abortada'))
);`
	if _, err := raw.Exec(legacySessoes + pacotesTable + eventosTable + etapasTable + pacotesSolicitadosTable); err != nil {
		raw.Close()
		t.Fatalf("criar schema legado: %v", err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := raw.Exec(`INSERT INTO sessoes (
		id, iniciada_em, so_destino, versao_destino, arch_destino,
		metodo_fetch, include_optional, status
	) VALUES (1, ?, 'ubuntu', '22.04', 'amd64', 'docker', 0, 'concluida')`,
		now,
	); err != nil {
		raw.Close()
		t.Fatalf("inserir sessão legada: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO pacotes (id, sessao_id, nome, versao, status) VALUES (1, 1, 'vim', '9.0', 'instalado')`); err != nil {
		raw.Close()
		t.Fatalf("inserir pacote legado: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO eventos (id, sessao_id, timestamp, tipo, mensagem) VALUES (1, 1, ?, 'download', 'ok')`, now); err != nil {
		raw.Close()
		t.Fatalf("inserir evento legado: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO etapas_sessao (sessao_id, etapa, nome, status) VALUES (1, 1, 'init', 'concluida')`); err != nil {
		raw.Close()
		t.Fatalf("inserir etapa legada: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO pacotes_solicitados (id, sessao_id, nome, status) VALUES (1, 1, 'vim', 'instalado')`); err != nil {
		raw.Close()
		t.Fatalf("inserir solicitado legado: %v", err)
	}
	_ = raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open com migração podman: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Ensure no table references a nonexistent "sessoes_old" table after rebuild.
	rows, err := st.db.QueryContext(ctx, "SELECT name, sql FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatalf("inspecionar sqlite_master: %v", err)
	}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			rows.Close()
			t.Fatalf("scan sqlite_master: %v", err)
		}
		if strings.Contains(strings.ToLower(ddl), "sessoes_old") {
			rows.Close()
			t.Fatalf("tabela %s ainda referencia sessoes_old: %s", name, ddl)
		}
	}
	rows.Close()

	existing, err := st.ObterSessao(ctx, 1)
	if err != nil || existing.FetchMethod != "docker" {
		t.Fatalf("sessão legada não preservada: %+v, err=%v", existing, err)
	}
	if pkgs, err := st.ListarPacotes(ctx, 1); err != nil || len(pkgs) != 1 {
		t.Fatalf("pacotes legados não preservados: %+v, err=%v", pkgs, err)
	}

	podman := newTestSession()
	podman.FetchMethod = "podman"
	if err := st.CreateSessao(ctx, podman); err != nil {
		t.Fatalf("CreateSessao(podman) após migração: %v", err)
	}
	// Writing child rows must succeed with foreign_keys=ON.
	if err := st.AdicionarPacote(ctx, &core.Pacote{SessaoID: podman.ID, Nome: "curl", Versao: "8.0", Status: core.PacoteBaixado}); err != nil {
		t.Fatalf("AdicionarPacote após migração: %v", err)
	}
	if err := st.AdicionarEvento(ctx, &core.Evento{SessaoID: podman.ID, Tipo: core.EventoDownload, Mensagem: "ok"}); err != nil {
		t.Fatalf("AdicionarEvento após migração: %v", err)
	}
	if err := st.RegistrarEtapa(ctx, &core.EtapaSessao{SessaoID: podman.ID, Etapa: 1, Status: core.EtapaConcluida}); err != nil {
		t.Fatalf("RegistrarEtapa após migração: %v", err)
	}
	if err := st.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{SessaoID: podman.ID, Nome: "curl"}); err != nil {
		t.Fatalf("AdicionarPacoteSolicitado após migração: %v", err)
	}
}

func TestReparoReferenciasSessoesOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken-v040.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("abrir SQLite: %v", err)
	}
	// Simulate a database previously broken by v0.4.0's ALTER TABLE sessoes RENAME TO sessoes_old.
	brokenSchema := sessoesTable +
		strings.ReplaceAll(pacotesTable, "REFERENCES sessoes(id)", `REFERENCES "sessoes_old"(id)`) +
		strings.ReplaceAll(eventosTable, "REFERENCES sessoes(id)", `REFERENCES "sessoes_old"(id)`) +
		strings.ReplaceAll(etapasTable, "REFERENCES sessoes(id)", `REFERENCES "sessoes_old"(id)`) +
		strings.ReplaceAll(pacotesSolicitadosTable, "REFERENCES sessoes(id)", `REFERENCES "sessoes_old"(id)`)
	if _, err := raw.Exec(brokenSchema); err != nil {
		raw.Close()
		t.Fatalf("criar schema corrompido simulado: %v", err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := raw.Exec(`INSERT INTO sessoes (id, iniciada_em, so_destino, versao_destino, arch_destino, metodo_fetch, status) VALUES (1, ?, 'ubuntu', '22.04', 'amd64', 'docker', 'concluida')`, now); err != nil {
		raw.Close()
		t.Fatalf("inserir sessao: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO pacotes (id, sessao_id, nome, versao, status) VALUES (1, 1, 'vim', '9.0', 'instalado')`); err != nil {
		raw.Close()
		t.Fatalf("inserir pacote: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO eventos (id, sessao_id, timestamp, tipo, mensagem) VALUES (1, 1, ?, 'download', 'ok')`, now); err != nil {
		raw.Close()
		t.Fatalf("inserir evento: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO etapas_sessao (sessao_id, etapa, nome, status) VALUES (1, 1, 'init', 'concluida')`); err != nil {
		raw.Close()
		t.Fatalf("inserir etapa: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO pacotes_solicitados (id, sessao_id, nome, status) VALUES (1, 1, 'vim', 'instalado')`); err != nil {
		raw.Close()
		t.Fatalf("inserir solicitado: %v", err)
	}
	_ = raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open com reparo automático: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if pkgs, err := st.ListarPacotes(ctx, 1); err != nil || len(pkgs) != 1 || pkgs[0].Nome != "vim" {
		t.Fatalf("pacotes não preservados após reparo: %+v, err=%v", pkgs, err)
	}
	if evs, err := st.ListarEventos(ctx, 1); err != nil || len(evs) != 1 {
		t.Fatalf("eventos não preservados após reparo: %+v, err=%v", evs, err)
	}
	if err := st.AdicionarPacote(ctx, &core.Pacote{SessaoID: 1, Nome: "curl", Versao: "8.0", Status: core.PacoteBaixado}); err != nil {
		t.Fatalf("AdicionarPacote após reparo falhou: %v", err)
	}
	if err := st.AdicionarEvento(ctx, &core.Evento{SessaoID: 1, Tipo: core.EventoDownload, Mensagem: "repaired"}); err != nil {
		t.Fatalf("AdicionarEvento após reparo falhou: %v", err)
	}
	if err := st.RegistrarEtapa(ctx, &core.EtapaSessao{SessaoID: 1, Etapa: 2, Status: core.EtapaConcluida}); err != nil {
		t.Fatalf("RegistrarEtapa após reparo falhou: %v", err)
	}
	if err := st.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{SessaoID: 1, Nome: "curl"}); err != nil {
		t.Fatalf("AdicionarPacoteSolicitado após reparo falhou: %v", err)
	}
}

func TestValidacoes(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	invalidMethod := newTestSession()
	invalidMethod.FetchMethod = "lxc"
	if err := store.CreateSessao(ctx, invalidMethod); err == nil {
		t.Fatal("esperava erro para método de fetch inválido")
	}

	podmanSession := newTestSession()
	podmanSession.FetchMethod = "podman"
	if err := store.CreateSessao(ctx, podmanSession); err != nil {
		t.Fatalf("CreateSessao(podman): %v", err)
	}

	sessao := newTestSession()
	if err := store.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	if err := store.FinalizarSessao(ctx, sessao.ID, core.SessaoEmAndamento, time.Now()); err == nil {
		t.Fatal("esperava erro para status final inválido")
	}

	pacote := &core.Pacote{SessaoID: sessao.ID, Nome: "vim", Versao: "1", Status: "desconhecido"}
	if err := store.AdicionarPacote(ctx, pacote); err == nil {
		t.Fatal("esperava erro para status de pacote inválido")
	}
}
