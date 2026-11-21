package security

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"

	"opf/internal/core"
	"opf/internal/store"
)

var sqlPayloads = []string{
	`x'; DROP TABLE pacotes; --`,
	`x'); DROP TABLE sessoes; --`,
	`x'; DROP TABLE eventos; --`,
	`1 OR 1=1; --`,
	`" OR "1"="1`,
	`x' UNION SELECT 1,2,3,4,5; --`,
	`\'; DROP TABLE etapas_sessao; --`,
}

// TestSQLInjectionStore drives SQL payloads through every user-controlled text
// field of the persistence layer and asserts the values are stored literally
// while the schema stays intact.
func TestSQLInjectionStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opf.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	sessao := &core.Sessao{
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		FetchMethod:   "docker",
	}
	if err := st.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}

	for index, payload := range sqlPayloads {
		pkg := &core.Pacote{
			SessaoID:     sessao.ID,
			Nome:         payload,
			Versao:       payload,
			Arquitetura:  "amd64",
			Status:       core.PacoteBaixado,
			CaminhoLocal: "/tmp/" + string(rune('a'+index)) + ".deb",
		}
		if err := st.AdicionarPacote(ctx, pkg); err != nil {
			t.Fatalf("AdicionarPacote(%q): %v", payload, err)
		}
		if err := st.AdicionarEvento(ctx, &core.Evento{
			SessaoID: sessao.ID,
			Tipo:     core.EventoDownload,
			Mensagem: payload,
			Detalhes: `{"payload":"` + payload + `"}`,
		}); err != nil {
			t.Fatalf("AdicionarEvento(%q): %v", payload, err)
		}
		if err := st.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{
			SessaoID: sessao.ID, Nome: payload,
		}); err != nil {
			t.Fatalf("AdicionarPacoteSolicitado(%q): %v", payload, err)
		}
	}

	pacotes, err := st.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes after payloads: %v", err)
	}
	if len(pacotes) != len(sqlPayloads) {
		t.Fatalf("pacotes = %d, want %d", len(pacotes), len(sqlPayloads))
	}
	for _, pkg := range pacotes {
		if pkg.Nome != pkg.Versao {
			t.Fatalf("payload not stored literally: %+v", pkg)
		}
		if pkg.Nome != sqlPayloads[0] && !containsString(sqlPayloads, pkg.Nome) {
			t.Fatalf("unexpected package value %q", pkg.Nome)
		}
	}

	eventos, err := st.ListarEventos(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarEventos after payloads: %v", err)
	}
	if len(eventos) != len(sqlPayloads) {
		t.Fatalf("eventos = %d, want %d", len(eventos), len(sqlPayloads))
	}
	for _, evento := range eventos {
		if !containsString(sqlPayloads, evento.Mensagem) {
			t.Fatalf("event payload not stored literally: %q", evento.Mensagem)
		}
	}

	solicitados, err := st.ListarPacotesSolicitados(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotesSolicitados after payloads: %v", err)
	}
	if len(solicitados) != len(sqlPayloads) {
		t.Fatalf("solicitados = %d, want %d", len(solicitados), len(sqlPayloads))
	}

	// Session fields are parameterized too: a payload in a user-visible field is
	// stored literally and the row remains readable.
	injectedSession := &core.Sessao{
		TargetOS:      `ubuntu'; DROP TABLE sessoes; --`,
		TargetVersion: `22.04"; DROP TABLE pacotes; --`,
		TargetArch:    "amd64",
		FetchMethod:   "docker",
	}
	if err := st.CreateSessao(ctx, injectedSession); err != nil {
		t.Fatalf("CreateSessao with payload: %v", err)
	}
	reloaded, err := st.ObterSessao(ctx, injectedSession.ID)
	if err != nil {
		t.Fatalf("ObterSessao after payload: %v", err)
	}
	if reloaded.TargetOS != injectedSession.TargetOS || reloaded.TargetVersion != injectedSession.TargetVersion {
		t.Fatalf("session payload altered: %+v", reloaded)
	}

	// Verification metadata is updated through dynamically built SET clauses;
	// assert values are still bound as parameters, never interpolated.
	shas := []string{`p1'; DROP TABLE pacotes; --`, `p2' OR '1'='1`}
	if err := st.AtualizarPacoteVerificacoes(ctx, pacotes[0].ID, shas[0], shas[1], nil, nil); err != nil {
		t.Fatalf("AtualizarPacoteVerificacoes: %v", err)
	}
	updated, err := st.ListarPacotes(ctx, sessao.ID)
	if err != nil {
		t.Fatalf("ListarPacotes after update: %v", err)
	}
	found := false
	for _, pkg := range updated {
		if pkg.ID == pacotes[0].ID {
			found = true
			if pkg.SHA256P1 != shas[0] || pkg.SHA256P2 != shas[1] {
				t.Fatalf("sha payload not stored literally: %+v", pkg)
			}
		}
	}
	if !found {
		t.Fatal("updated package missing")
	}

	// The schema must be intact after every payload. A second connection reads
	// sqlite_master so the assertion is independent from the store wrapper.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer raw.Close()
	rows, err := raw.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	sort.Strings(tables)
	for _, want := range []string{"etapas_sessao", "eventos", "pacotes", "pacotes_solicitados", "sessoes"} {
		if !containsString(tables, want) {
			t.Fatalf("table %q missing after SQL payloads; tables=%v", want, tables)
		}
	}

	// Table content survived: an extra row can still be inserted and listed.
	if err := st.AdicionarPacote(ctx, &core.Pacote{
		SessaoID: sessao.ID, Nome: "post-payload", Versao: "1.0", Status: core.PacoteBaixado,
	}); err != nil {
		t.Fatalf("insert after payloads: %v", err)
	}
	if _, err := st.ListarPacotes(ctx, sessao.ID); err != nil {
		t.Fatalf("list after payloads: %v", err)
	}
}

// TestStoreRejectsInvalidStatusDespiteInjection asserts enum validation happens
// before the statement runs, so no payload can reach the SQL text.
func TestStoreRejectsInvalidStatusDespiteInjection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	sessao := &core.Sessao{TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64", FetchMethod: "docker"}
	if err := st.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	for _, status := range []core.PacoteStatus{
		`baixado'; DROP TABLE pacotes; --`,
		core.PacoteStatus(`1 OR 1=1`),
	} {
		if err := st.AdicionarPacote(ctx, &core.Pacote{
			SessaoID: sessao.ID, Nome: "vim", Versao: "1", Status: status,
		}); err == nil {
			t.Errorf("invalid status %q accepted", status)
		}
	}
}
