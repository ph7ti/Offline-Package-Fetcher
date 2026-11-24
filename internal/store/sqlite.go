package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opf/internal/core"

	_ "modernc.org/sqlite"
)

const (
	sessoesTable = `
CREATE TABLE IF NOT EXISTS sessoes (
    id              INTEGER PRIMARY KEY,
    iniciada_em     DATETIME NOT NULL,
    finalizada_em   DATETIME,
    so_destino      TEXT NOT NULL,
    versao_destino  TEXT NOT NULL,
    arch_destino    TEXT NOT NULL,
    metodo_fetch    TEXT NOT NULL CHECK (metodo_fetch IN ('docker','podman','wsl')),
    host_destino    TEXT,
    include_optional INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL CHECK (status IN
                      ('em_andamento','concluida','falhou','abortada')),
    etapa_atual     INTEGER NOT NULL DEFAULT 1,
    output_dir      TEXT,
    remote_dir      TEXT,
    imagem_docker   TEXT,
    wsl_distro      TEXT,
    ambiente_id     TEXT,
    config_json     TEXT,
    host_info_json  TEXT,
    preview_json    TEXT,
    arquivada       INTEGER NOT NULL DEFAULT 0
);
`

	pacotesTable = `
CREATE TABLE IF NOT EXISTS pacotes (
    id              INTEGER PRIMARY KEY,
    sessao_id       INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    nome            TEXT NOT NULL,
    versao          TEXT NOT NULL,
    arquitetura     TEXT,
    status          TEXT NOT NULL CHECK (status IN
                      ('baixado','enviado','enviado_nao_instalado','instalado','falhou','ja_instalado')),
    eh_dependencia  INTEGER NOT NULL DEFAULT 0,
    caminho_local   TEXT,
    sha256          TEXT,
    sha256_p1       TEXT,
    sha256_p2       TEXT,
    versao_anterior TEXT,
    p3_verificado   INTEGER NOT NULL DEFAULT 0,
    instalado_verificado INTEGER NOT NULL DEFAULT 0
);
`

	eventosTable = `
CREATE TABLE IF NOT EXISTS eventos (
    id          INTEGER PRIMARY KEY,
    sessao_id   INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    timestamp   DATETIME NOT NULL,
    tipo        TEXT NOT NULL,
    mensagem    TEXT NOT NULL,
    detalhes    TEXT
);
`

	etapasTable = `
CREATE TABLE IF NOT EXISTS etapas_sessao (
    sessao_id      INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    etapa          INTEGER NOT NULL,
    nome           TEXT NOT NULL,
    status         TEXT NOT NULL,
    iniciada_em    DATETIME,
    finalizada_em  DATETIME,
    erro           TEXT,
    PRIMARY KEY (sessao_id, etapa)
);
`

	pacotesSolicitadosTable = `
CREATE TABLE IF NOT EXISTS pacotes_solicitados (
    id          INTEGER PRIMARY KEY,
    sessao_id   INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    nome        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pendente',
    motivo      TEXT
);
`

	schemaIndexes = `
CREATE INDEX IF NOT EXISTS idx_pacotes_sessao ON pacotes(sessao_id);
CREATE INDEX IF NOT EXISTS idx_eventos_sessao ON eventos(sessao_id);
CREATE INDEX IF NOT EXISTS idx_etapas_sessao ON etapas_sessao(sessao_id);
CREATE INDEX IF NOT EXISTS idx_solicitados_sessao ON pacotes_solicitados(sessao_id);
`

	schema = "PRAGMA journal_mode = WAL;\n" + sessoesTable + pacotesTable + eventosTable + etapasTable + pacotesSolicitadosTable + schemaIndexes
)

// Store is the SQLite implementation of core.Repository.
type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("caminho do SQLite não informado")
	}

	if path != ":memory:" {
		dir := filepath.Dir(path)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("criar diretório do SQLite: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("abrir SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("habilitar foreign_keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configurar busy_timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criar schema SQLite: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrateSessoesFetchMethod(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migratePacotesStatus(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.ensureColumns(ctx, "sessoes", map[string]string{
		"etapa_atual":    "INTEGER NOT NULL DEFAULT 1",
		"output_dir":     "TEXT",
		"remote_dir":     "TEXT",
		"imagem_docker":  "TEXT",
		"wsl_distro":     "TEXT",
		"ambiente_id":    "TEXT",
		"config_json":    "TEXT",
		"host_info_json": "TEXT",
		"preview_json":   "TEXT",
		"arquivada":      "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.ensureColumns(ctx, "pacotes", map[string]string{
		"sha256_p1":            "TEXT",
		"sha256_p2":            "TEXT",
		"versao_anterior":      "TEXT",
		"p3_verificado":        "INTEGER NOT NULL DEFAULT 0",
		"instalado_verificado": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) ensureColumns(ctx context.Context, table string, columns map[string]string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspecionar colunas de %s: %w", table, err)
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("ler coluna de %s: %w", table, err)
		}
		existing[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("fechar inspeção de %s: %w", table, err)
	}
	for name, definition := range columns {
		if _, ok := existing[name]; ok {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, definition)
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("adicionar coluna %s.%s: %w", table, name, err)
		}
	}
	return nil
}

func (s *Store) migrateSessoesFetchMethod(ctx context.Context) error {
	var ddl string
	err := s.db.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessoes'",
	).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecionar schema de sessoes: %w", err)
	}
	if strings.Contains(ddl, "'podman'") {
		return nil
	}

	// Ensure all modern columns exist on the legacy table before copying so the
	// SELECT list below is always valid even for v0.1.0 databases.
	if err := s.ensureColumns(ctx, "sessoes", map[string]string{
		"etapa_atual":    "INTEGER NOT NULL DEFAULT 1",
		"output_dir":     "TEXT",
		"remote_dir":     "TEXT",
		"imagem_docker":  "TEXT",
		"wsl_distro":     "TEXT",
		"ambiente_id":    "TEXT",
		"config_json":    "TEXT",
		"host_info_json": "TEXT",
		"preview_json":   "TEXT",
		"arquivada":      "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("desabilitar foreign_keys na migração de sessoes: %w", err)
	}
	defer func() {
		_, _ = s.db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
	}()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciar migração de sessoes: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		"DROP TABLE IF EXISTS sessoes_old",
		"ALTER TABLE sessoes RENAME TO sessoes_old",
		sessoesTable,
		`INSERT INTO sessoes (
			id, iniciada_em, finalizada_em, so_destino, versao_destino, arch_destino,
			metodo_fetch, host_destino, include_optional, status, etapa_atual,
			output_dir, remote_dir, imagem_docker, wsl_distro, ambiente_id,
			config_json, host_info_json, preview_json, arquivada
		) SELECT
			id, iniciada_em, finalizada_em, so_destino, versao_destino, arch_destino,
			metodo_fetch, host_destino, include_optional, status, etapa_atual,
			output_dir, remote_dir, imagem_docker, wsl_distro, ambiente_id,
			config_json, host_info_json, preview_json, arquivada
		FROM sessoes_old`,
		"DROP TABLE sessoes_old",
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrar tabela sessoes: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmar migração de sessoes: %w", err)
	}
	return nil
}

func (s *Store) migratePacotesStatus(ctx context.Context) error {
	var ddl string
	err := s.db.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'pacotes'",
	).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecionar schema de pacotes: %w", err)
	}
	if strings.Contains(ddl, "'enviado_nao_instalado'") {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciar migração de pacotes: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		"DROP INDEX IF EXISTS idx_pacotes_sessao",
		"DROP TABLE IF EXISTS pacotes_old",
		"ALTER TABLE pacotes RENAME TO pacotes_old",
		pacotesTable,
		`INSERT INTO pacotes (
			id, sessao_id, nome, versao, arquitetura, status,
			eh_dependencia, caminho_local, sha256
		) SELECT
			id, sessao_id, nome, versao, arquitetura, status,
			eh_dependencia, caminho_local, sha256
		FROM pacotes_old`,
		"DROP TABLE pacotes_old",
		"CREATE INDEX IF NOT EXISTS idx_pacotes_sessao ON pacotes(sessao_id)",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrar schema de pacotes: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmar migração de pacotes: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) CreateSessao(ctx context.Context, sessao *core.Sessao) error {
	if sessao == nil {
		return errors.New("sessao nula")
	}
	if err := sessao.Validate(); err != nil {
		return err
	}
	if sessao.IniciadaEm.IsZero() {
		sessao.IniciadaEm = time.Now().UTC()
	}
	if sessao.Status == "" {
		sessao.Status = core.SessaoEmAndamento
	}
	if sessao.EtapaAtual == 0 {
		sessao.EtapaAtual = 1
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sessoes (
			iniciada_em, finalizada_em, so_destino, versao_destino,
			arch_destino, metodo_fetch, host_destino, include_optional, status,
			etapa_atual, output_dir, remote_dir, imagem_docker, wsl_distro,
			ambiente_id, config_json, host_info_json, preview_json, arquivada
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(sessao.IniciadaEm),
		nullableTime(sessao.FinalizadaEm),
		sessao.TargetOS,
		sessao.TargetVersion,
		sessao.TargetArch,
		sessao.FetchMethod,
		nullString(sessao.HostDestino),
		boolInt(sessao.IncludeOptional),
		string(sessao.Status),
		sessao.EtapaAtual,
		nullString(sessao.OutputDir),
		nullString(sessao.RemoteDir),
		nullString(sessao.ImagemDocker),
		nullString(sessao.WSLDistro),
		nullString(sessao.AmbienteID),
		nullString(sessao.ConfigJSON),
		nullString(sessao.HostInfoJSON),
		nullString(sessao.PreviewJSON),
		boolInt(sessao.Arquivada),
	)
	if err != nil {
		return fmt.Errorf("inserir sessão: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("obter id da sessão: %w", err)
	}
	sessao.ID = id
	return nil
}

func (s *Store) ObterSessao(ctx context.Context, id int64) (*core.Sessao, error) {
	var (
		sessao          core.Sessao
		iniciadaEm      string
		finalizadaEm    sql.NullString
		hostDestino     sql.NullString
		outputDir       sql.NullString
		remoteDir       sql.NullString
		imagemDocker    sql.NullString
		wslDistro       sql.NullString
		ambienteID      sql.NullString
		configJSON      sql.NullString
		hostInfoJSON    sql.NullString
		previewJSON     sql.NullString
		includeOptional int
		arquivada       int
		status          string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, iniciada_em, finalizada_em, so_destino, versao_destino,
		       arch_destino, metodo_fetch, host_destino, include_optional, status,
		       etapa_atual, output_dir, remote_dir, imagem_docker, wsl_distro,
		       ambiente_id, config_json, host_info_json, preview_json, arquivada
		FROM sessoes
		WHERE id = ?`, id).Scan(
		&sessao.ID, &iniciadaEm, &finalizadaEm, &sessao.TargetOS,
		&sessao.TargetVersion, &sessao.TargetArch, &sessao.FetchMethod,
		&hostDestino, &includeOptional, &status,
		&sessao.EtapaAtual, &outputDir, &remoteDir, &imagemDocker, &wslDistro,
		&ambienteID, &configJSON, &hostInfoJSON, &previewJSON, &arquivada,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sessão %d não encontrada", id)
	}
	if err != nil {
		return nil, fmt.Errorf("obter sessão %d: %w", id, err)
	}

	parsedInicio, err := parseTime(iniciadaEm)
	if err != nil {
		return nil, fmt.Errorf("ler início da sessão %d: %w", id, err)
	}
	sessao.IniciadaEm = parsedInicio
	if finalizadaEm.Valid {
		parsedFim, err := parseTime(finalizadaEm.String)
		if err != nil {
			return nil, fmt.Errorf("ler fim da sessão %d: %w", id, err)
		}
		sessao.FinalizadaEm = &parsedFim
	}
	sessao.HostDestino = hostDestino.String
	sessao.IncludeOptional = includeOptional != 0
	sessao.Status = core.SessaoStatus(status)
	sessao.OutputDir = outputDir.String
	sessao.RemoteDir = remoteDir.String
	sessao.ImagemDocker = imagemDocker.String
	sessao.WSLDistro = wslDistro.String
	sessao.AmbienteID = ambienteID.String
	sessao.ConfigJSON = configJSON.String
	sessao.HostInfoJSON = hostInfoJSON.String
	sessao.PreviewJSON = previewJSON.String
	sessao.Arquivada = arquivada != 0
	if sessao.EtapaAtual == 0 {
		sessao.EtapaAtual = 1
	}
	return &sessao, nil
}

func (s *Store) FinalizarSessao(ctx context.Context, id int64, status core.SessaoStatus, at time.Time) error {
	switch status {
	case core.SessaoConcluida, core.SessaoFalhou, core.SessaoAbortada:
	default:
		return fmt.Errorf("status final inválido: %q", status)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET status = ?, finalizada_em = ? WHERE id = ?",
		string(status), formatTime(at), id,
	)
	if err != nil {
		return fmt.Errorf("finalizar sessão %d: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("verificar sessão finalizada: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("sessão %d não encontrada", id)
	}
	return nil
}

func (s *Store) AdicionarPacote(ctx context.Context, p *core.Pacote) error {
	if p == nil {
		return errors.New("pacote nulo")
	}
	if p.SessaoID == 0 {
		return errors.New("pacote sem sessão")
	}
	if p.Nome == "" || p.Versao == "" {
		return errors.New("pacote sem nome ou versão")
	}
	if p.Status == "" {
		p.Status = core.PacoteBaixado
	}
	if err := validPacoteStatus(p.Status); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO pacotes (
			sessao_id, nome, versao, arquitetura, status,
			eh_dependencia, caminho_local, sha256, sha256_p1, sha256_p2,
			versao_anterior, p3_verificado, instalado_verificado
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.SessaoID,
		p.Nome,
		p.Versao,
		nullString(p.Arquitetura),
		string(p.Status),
		boolInt(p.EhDependencia),
		nullString(p.CaminhoLocal),
		nullString(p.SHA256),
		nullString(p.SHA256P1),
		nullString(p.SHA256P2),
		nullString(p.VersaoAnterior),
		boolInt(p.P3Verificado),
		boolInt(p.InstaladoVerificado),
	)
	if err != nil {
		return fmt.Errorf("inserir pacote %q: %w", p.Nome, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("obter id do pacote %q: %w", p.Nome, err)
	}
	p.ID = id
	return nil
}

func (s *Store) AtualizarPacotesStatus(ctx context.Context, sessaoID int64, de, para core.PacoteStatus) error {
	if sessaoID == 0 {
		return errors.New("atualização de pacotes sem sessão")
	}
	if err := validPacoteStatus(de); err != nil {
		return fmt.Errorf("status de origem: %w", err)
	}
	if err := validPacoteStatus(para); err != nil {
		return fmt.Errorf("status de destino: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE pacotes SET status = ? WHERE sessao_id = ? AND status = ?",
		string(para), sessaoID, string(de),
	); err != nil {
		return fmt.Errorf("atualizar status dos pacotes da sessão %d: %w", sessaoID, err)
	}
	return nil
}

func (s *Store) AdicionarEvento(ctx context.Context, e *core.Evento) error {
	if e == nil {
		return errors.New("evento nulo")
	}
	if e.SessaoID == 0 {
		return errors.New("evento sem sessão")
	}
	if e.Tipo == "" || e.Mensagem == "" {
		return errors.New("evento sem tipo ou mensagem")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO eventos (sessao_id, timestamp, tipo, mensagem, detalhes)
		VALUES (?, ?, ?, ?, ?)`,
		e.SessaoID,
		formatTime(e.Timestamp),
		e.Tipo,
		e.Mensagem,
		nullString(e.Detalhes),
	)
	if err != nil {
		return fmt.Errorf("inserir evento: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("obter id do evento: %w", err)
	}
	e.ID = id
	return nil
}

func (s *Store) ListarPacotes(ctx context.Context, sessaoID int64) ([]core.Pacote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sessao_id, nome, versao, arquitetura, status,
		       eh_dependencia, caminho_local, sha256, sha256_p1, sha256_p2,
		       versao_anterior, p3_verificado, instalado_verificado
		FROM pacotes
		WHERE sessao_id = ?
		ORDER BY nome`, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar pacotes: %w", err)
	}
	defer rows.Close()

	var pacotes []core.Pacote
	for rows.Next() {
		var (
			p              core.Pacote
			arquitetura    sql.NullString
			status         string
			caminho        sql.NullString
			sha256         sql.NullString
			sha256P1       sql.NullString
			sha256P2       sql.NullString
			versaoAnterior sql.NullString
			p3             int
			instalado      int
			dependencia    int
		)
		if err := rows.Scan(
			&p.ID, &p.SessaoID, &p.Nome, &p.Versao, &arquitetura, &status,
			&dependencia, &caminho, &sha256, &sha256P1, &sha256P2, &versaoAnterior, &p3, &instalado,
		); err != nil {
			return nil, fmt.Errorf("ler pacote: %w", err)
		}
		p.Arquitetura = arquitetura.String
		p.Status = core.PacoteStatus(status)
		p.EhDependencia = dependencia != 0
		p.CaminhoLocal = caminho.String
		p.SHA256 = sha256.String
		p.SHA256P1 = sha256P1.String
		p.SHA256P2 = sha256P2.String
		p.VersaoAnterior = versaoAnterior.String
		p.P3Verificado = p3 != 0
		p.InstaladoVerificado = instalado != 0
		pacotes = append(pacotes, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar pacotes: %w", err)
	}
	return pacotes, nil
}

func (s *Store) ListarEventos(ctx context.Context, sessaoID int64) ([]core.Evento, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sessao_id, timestamp, tipo, mensagem, detalhes
		FROM eventos
		WHERE sessao_id = ?
		ORDER BY id`, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar eventos: %w", err)
	}
	defer rows.Close()

	var eventos []core.Evento
	for rows.Next() {
		var (
			e        core.Evento
			ts       string
			detalhes sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.SessaoID, &ts, &e.Tipo, &e.Mensagem, &detalhes); err != nil {
			return nil, fmt.Errorf("ler evento: %w", err)
		}
		parsed, err := parseTime(ts)
		if err != nil {
			return nil, fmt.Errorf("ler timestamp do evento %d: %w", e.ID, err)
		}
		e.Timestamp = parsed
		e.Detalhes = detalhes.String
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar eventos: %w", err)
	}
	return eventos, nil
}

func (s *Store) ReabrirSessao(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET status = ?, finalizada_em = NULL WHERE id = ?",
		string(core.SessaoEmAndamento), id,
	); err != nil {
		return fmt.Errorf("reabrir sessão %d: %w", id, err)
	}
	return nil
}

func (s *Store) AtualizarSessaoExecucao(ctx context.Context, id int64, outputDir, remoteDir, imagem, wslDistro, ambienteID, configJSON string) error {
	if id == 0 {
		return errors.New("sessão não informada")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessoes
		SET output_dir = ?, remote_dir = ?, imagem_docker = ?, wsl_distro = ?,
		    ambiente_id = ?, config_json = ?
		WHERE id = ?`,
		nullString(outputDir),
		nullString(remoteDir),
		nullString(imagem),
		nullString(wslDistro),
		nullString(ambienteID),
		nullString(configJSON),
		id,
	)
	if err != nil {
		return fmt.Errorf("atualizar execução da sessão %d: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("verificar sessão atualizada: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("sessão %d não encontrada", id)
	}
	return nil
}

func (s *Store) AtualizarEtapaAtual(ctx context.Context, id int64, etapa int) error {
	if etapa < 0 || etapa > core.TotalEtapas {
		return fmt.Errorf("etapa inválida: %d", etapa)
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET etapa_atual = ? WHERE id = ?", etapa, id,
	); err != nil {
		return fmt.Errorf("atualizar etapa atual da sessão %d: %w", id, err)
	}
	return nil
}

func (s *Store) RegistrarEtapa(ctx context.Context, e *core.EtapaSessao) error {
	if e == nil || e.SessaoID == 0 || e.Etapa < 1 || e.Etapa > core.TotalEtapas {
		return errors.New("etapa de sessão inválida")
	}
	if e.Status == "" {
		e.Status = core.EtapaPendente
	}
	if e.Nome == "" {
		e.Nome = core.NomeEtapa(e.Etapa)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO etapas_sessao (
			sessao_id, etapa, nome, status, iniciada_em, finalizada_em, erro
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sessao_id, etapa) DO UPDATE SET
			nome = excluded.nome,
			status = excluded.status,
			iniciada_em = excluded.iniciada_em,
			finalizada_em = excluded.finalizada_em,
			erro = excluded.erro`,
		e.SessaoID,
		e.Etapa,
		e.Nome,
		string(e.Status),
		nullableTime(e.IniciadaEm),
		nullableTime(e.FinalizadaEm),
		nullString(e.Erro),
	)
	if err != nil {
		return fmt.Errorf("registrar etapa %d da sessão %d: %w", e.Etapa, e.SessaoID, err)
	}
	return nil
}

func (s *Store) ListarEtapas(ctx context.Context, sessaoID int64) ([]core.EtapaSessao, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sessao_id, etapa, nome, status, iniciada_em, finalizada_em, erro
		FROM etapas_sessao
		WHERE sessao_id = ?
		ORDER BY etapa`, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar etapas da sessão %d: %w", sessaoID, err)
	}
	defer rows.Close()
	var etapas []core.EtapaSessao
	for rows.Next() {
		var (
			e          core.EtapaSessao
			status     string
			iniciada   sql.NullString
			finalizada sql.NullString
			erro       sql.NullString
		)
		if err := rows.Scan(&e.SessaoID, &e.Etapa, &e.Nome, &status, &iniciada, &finalizada, &erro); err != nil {
			return nil, fmt.Errorf("ler etapa: %w", err)
		}
		e.Status = core.EtapaStatus(status)
		e.Erro = erro.String
		if iniciada.Valid {
			if t, err := parseTime(iniciada.String); err == nil {
				e.IniciadaEm = &t
			}
		}
		if finalizada.Valid {
			if t, err := parseTime(finalizada.String); err == nil {
				e.FinalizadaEm = &t
			}
		}
		etapas = append(etapas, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar etapas: %w", err)
	}
	return etapas, nil
}

func (s *Store) AdicionarPacoteSolicitado(ctx context.Context, p *core.PacoteSolicitado) error {
	if p == nil || p.SessaoID == 0 || p.Nome == "" {
		return errors.New("pacote solicitado inválido")
	}
	if p.Status == "" {
		p.Status = "pendente"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO pacotes_solicitados (sessao_id, nome, status, motivo)
		VALUES (?, ?, ?, ?)`,
		p.SessaoID, p.Nome, p.Status, nullString(p.Motivo),
	)
	if err != nil {
		return fmt.Errorf("inserir pacote solicitado %q: %w", p.Nome, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("obter id do pacote solicitado: %w", err)
	}
	p.ID = id
	return nil
}

func (s *Store) ListarPacotesSolicitados(ctx context.Context, sessaoID int64) ([]core.PacoteSolicitado, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sessao_id, nome, status, motivo
		FROM pacotes_solicitados
		WHERE sessao_id = ?
		ORDER BY nome`, sessaoID)
	if err != nil {
		return nil, fmt.Errorf("listar pacotes solicitados: %w", err)
	}
	defer rows.Close()
	var pacotes []core.PacoteSolicitado
	for rows.Next() {
		var (
			p      core.PacoteSolicitado
			motivo sql.NullString
		)
		if err := rows.Scan(&p.ID, &p.SessaoID, &p.Nome, &p.Status, &motivo); err != nil {
			return nil, fmt.Errorf("ler pacote solicitado: %w", err)
		}
		p.Motivo = motivo.String
		pacotes = append(pacotes, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar pacotes solicitados: %w", err)
	}
	return pacotes, nil
}

func (s *Store) AtualizarPacoteVerificacoes(ctx context.Context, pacoteID int64, shaP1, shaP2 string, p3, instalado *bool) error {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if shaP1 != "" {
		sets = append(sets, "sha256_p1 = ?")
		args = append(args, shaP1)
	}
	if shaP2 != "" {
		sets = append(sets, "sha256_p2 = ?")
		args = append(args, shaP2)
	}
	if p3 != nil {
		sets = append(sets, "p3_verificado = ?")
		args = append(args, boolInt(*p3))
	}
	if instalado != nil {
		sets = append(sets, "instalado_verificado = ?")
		args = append(args, boolInt(*instalado))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, pacoteID)
	if _, err := s.db.ExecContext(ctx,
		"UPDATE pacotes SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...,
	); err != nil {
		return fmt.Errorf("atualizar verificações do pacote %d: %w", pacoteID, err)
	}
	return nil
}

func (s *Store) AtualizarPacoteVersaoAnterior(ctx context.Context, pacoteID int64, versao string) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE pacotes SET versao_anterior = ? WHERE id = ?", nullString(versao), pacoteID,
	); err != nil {
		return fmt.Errorf("atualizar versão anterior do pacote %d: %w", pacoteID, err)
	}
	return nil
}

func (s *Store) AtualizarHostInfo(ctx context.Context, sessaoID int64, infoJSON string) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET host_info_json = ? WHERE id = ?", nullString(infoJSON), sessaoID,
	); err != nil {
		return fmt.Errorf("atualizar host info da sessão %d: %w", sessaoID, err)
	}
	return nil
}

func (s *Store) AtualizarSessaoPreview(ctx context.Context, sessaoID int64, previewJSON string) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET preview_json = ? WHERE id = ?", nullString(previewJSON), sessaoID,
	); err != nil {
		return fmt.Errorf("atualizar preview da sessão %d: %w", sessaoID, err)
	}
	return nil
}

func (s *Store) ListarSessoes(ctx context.Context, somenteNaoArquivadas bool) ([]core.SessaoResumo, error) {
	query := `
		SELECT s.id, s.iniciada_em, s.finalizada_em, s.so_destino, s.versao_destino,
		       s.arch_destino, s.metodo_fetch, s.host_destino, s.include_optional,
		       s.status, s.etapa_atual, s.output_dir, s.remote_dir, s.imagem_docker,
		       s.wsl_distro, s.ambiente_id, s.config_json, s.arquivada,
		       COALESCE(p.total, 0), COALESCE(p.instalados, 0), COALESCE(p.deps, 0)
		FROM sessoes s
		LEFT JOIN (
			SELECT sessao_id,
			       COUNT(*) AS total,
			       SUM(CASE WHEN status = 'instalado' THEN 1 ELSE 0 END) AS instalados,
			       SUM(CASE WHEN eh_dependencia = 1 THEN 1 ELSE 0 END) AS deps
			FROM pacotes
			GROUP BY sessao_id
		) p ON p.sessao_id = s.id`
	if somenteNaoArquivadas {
		query += " WHERE s.arquivada = 0"
	}
	query += " ORDER BY s.id DESC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listar sessões: %w", err)
	}
	defer rows.Close()
	var resumos []core.SessaoResumo
	for rows.Next() {
		var (
			r               core.SessaoResumo
			iniciada        string
			finalizada      sql.NullString
			host            sql.NullString
			outputDir       sql.NullString
			remoteDir       sql.NullString
			imagem          sql.NullString
			wslDistro       sql.NullString
			ambienteID      sql.NullString
			configJSON      sql.NullString
			includeOptional int
			arquivada       int
			status          string
		)
		if err := rows.Scan(
			&r.ID, &iniciada, &finalizada, &r.TargetOS, &r.TargetVersion,
			&r.TargetArch, &r.FetchMethod, &host, &includeOptional, &status,
			&r.EtapaAtual, &outputDir, &remoteDir, &imagem, &wslDistro,
			&ambienteID, &configJSON, &arquivada, &r.TotalPacotes,
			&r.TotalInstalados, &r.TotalDependencias,
		); err != nil {
			return nil, fmt.Errorf("ler sessão resumida: %w", err)
		}
		if t, err := parseTime(iniciada); err == nil {
			r.IniciadaEm = t
		}
		if finalizada.Valid {
			if t, err := parseTime(finalizada.String); err == nil {
				r.FinalizadaEm = &t
			}
		}
		r.HostDestino = host.String
		r.IncludeOptional = includeOptional != 0
		r.Status = core.SessaoStatus(status)
		r.OutputDir = outputDir.String
		r.RemoteDir = remoteDir.String
		r.ImagemDocker = imagem.String
		r.WSLDistro = wslDistro.String
		r.AmbienteID = ambienteID.String
		r.ConfigJSON = configJSON.String
		r.Arquivada = arquivada != 0
		resumos = append(resumos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar sessões: %w", err)
	}
	return resumos, nil
}

func (s *Store) ArquivarSessao(ctx context.Context, id int64, arquivada bool) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE sessoes SET arquivada = ? WHERE id = ?", boolInt(arquivada), id,
	); err != nil {
		return fmt.Errorf("arquivar sessão %d: %w", id, err)
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("formato de data inválido: %q", value)
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validPacoteStatus(status core.PacoteStatus) error {
	switch status {
	case core.PacoteBaixado,
		core.PacoteEnviado,
		core.PacoteEnviadoNaoInstalado,
		core.PacoteInstalado,
		core.PacoteFalhou,
		core.PacoteJaInstalado:
		return nil
	default:
		return fmt.Errorf("status de pacote inválido: %q", status)
	}
}
