package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opf/internal/core"
	"opf/internal/store"
)

func TestSyslogUDP(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	sink, err := NewSyslogSink(SyslogConfig{Enabled: true, Host: "127.0.0.1", Port: addr.Port, Proto: "udp", Tag: "opftest"})
	if err != nil {
		t.Fatalf("NewSyslogSink: %v", err)
	}
	defer sink.Close(context.Background())
	event := core.Evento{SessaoID: 7, Timestamp: time.Now().UTC(), Tipo: core.EventoInfo, Mensagem: "teste syslog", Detalhes: `{"pacotes":3}`}
	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	line := string(buf[:n])
	for _, want := range []string{"<14>1", "opftest", "teste syslog", `"sessao_id":7`} {
		if !strings.Contains(line, want) {
			t.Fatalf("mensagem sem %q: %s", want, line)
		}
	}
	if strings.Contains(strings.ToLower(line), "password") {
		t.Fatal("mensagem contém possível segredo")
	}
}

func TestSyslogTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, _ := bufio.NewReader(conn).ReadString('\n')
		received <- line
	}()
	addr := listener.Addr().(*net.TCPAddr)
	sink, err := NewSyslogSink(SyslogConfig{Enabled: true, Host: "127.0.0.1", Port: addr.Port, Proto: "tcp", Tag: "opftest"})
	if err != nil {
		t.Fatalf("NewSyslogSink: %v", err)
	}
	defer sink.Close(context.Background())
	if err := sink.Emit(context.Background(), core.Evento{SessaoID: 1, Tipo: core.EventoErro, Mensagem: "falha"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	select {
	case line := <-received:
		if !strings.Contains(line, "<11>1") || !strings.Contains(line, "falha") {
			t.Fatalf("linha: %q", line)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timeout TCP")
	}
}

func TestSyslogDisabled(t *testing.T) {
	sink, err := NewSyslogSink(SyslogConfig{Enabled: false})
	if err != nil || sink != nil {
		t.Fatalf("sink=%v err=%v", sink, err)
	}
}

func TestSendRelatorioSyslog(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	relatorio := &Relatorio{Sessao: core.Sessao{ID: 9}, GeradoEm: time.Now().UTC()}
	if err := SendRelatorioSyslog(context.Background(), SyslogConfig{Enabled: true, Host: "127.0.0.1", Port: addr.Port, Proto: "udp", Tag: "opftest"}, relatorio); err != nil {
		t.Fatalf("SendRelatorioSyslog: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "relatório da sessão #9") {
		t.Fatalf("mensagem: %s", buf[:n])
	}
}

func TestRelatorioExport(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	sessao := &core.Sessao{TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64", FetchMethod: "docker"}
	if err := db.CreateSessao(ctx, sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	if err := db.AtualizarHostInfo(ctx, sessao.ID, `{"host":"target","usuario":"opf","kernel":"6.8.0","uptime":"1d","cpu":"4 cores","memoria":"8 GB","disco":"100 GB"}`); err != nil {
		t.Fatalf("AtualizarHostInfo: %v", err)
	}
	if err := db.AdicionarPacoteSolicitado(ctx, &core.PacoteSolicitado{SessaoID: sessao.ID, Nome: "vim"}); err != nil {
		t.Fatalf("AdicionarPacoteSolicitado: %v", err)
	}
	if err := db.AdicionarPacote(ctx, &core.Pacote{SessaoID: sessao.ID, Nome: "vim", Versao: "9.0", Arquitetura: "amd64", Status: core.PacoteInstalado, VersaoAnterior: "8.0", SHA256: "abc"}); err != nil {
		t.Fatalf("AdicionarPacote: %v", err)
	}
	if err := db.AdicionarPacote(ctx, &core.Pacote{SessaoID: sessao.ID, Nome: "libc6", Versao: "2.35", Arquitetura: "amd64", Status: core.PacoteInstalado, EhDependencia: true, SHA256: "def"}); err != nil {
		t.Fatalf("AdicionarPacote dep: %v", err)
	}
	if err := db.AdicionarEvento(ctx, &core.Evento{SessaoID: sessao.ID, Tipo: core.EventoInfo, Mensagem: "sessão teste"}); err != nil {
		t.Fatalf("AdicionarEvento: %v", err)
	}

	relatorio, err := GerarRelatorio(ctx, db, sessao.ID)
	if err != nil {
		t.Fatalf("GerarRelatorio: %v", err)
	}
	data, err := relatorio.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if decoded["gerado_em"] == nil {
		t.Fatal("JSON sem gerado_em")
	}
	markdown := relatorio.Markdown()
	for _, want := range []string{"# Relatório", "## Host remoto", "6.8.0", "Usuário SSH", "opf", "## Pacotes solicitados", "principal", "dependente", "8.0", "sessão teste", "instalado"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown sem %q", want)
		}
	}
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "rel.json")
	mdPath := filepath.Join(dir, "rel.md")
	if err := SalvarJSON(jsonPath, relatorio); err != nil {
		t.Fatalf("SalvarJSON: %v", err)
	}
	if err := SalvarMarkdown(mdPath, relatorio); err != nil {
		t.Fatalf("SalvarMarkdown: %v", err)
	}
	if info, err := os.Stat(jsonPath); err != nil || info.Size() == 0 {
		t.Fatalf("JSON não gerado: %v", err)
	}
	if info, err := os.Stat(mdPath); err != nil || info.Size() == 0 {
		t.Fatalf("Markdown não gerado: %v", err)
	}
}
