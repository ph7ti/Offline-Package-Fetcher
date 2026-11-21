package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"opf/internal/core"
)

// TestSecuritySyslogFormatSanitizesMessage asserts event text cannot inject
// extra RFC 5424 lines or terminal control sequences: the payload is JSON
// encoded, so newlines/CR/NUL/ANSI become escapes on a single physical line.
func TestSecuritySyslogFormatSanitizesMessage(t *testing.T) {
	sink := &SyslogSink{
		cfg:      SyslogConfig{Tag: "opf"},
		hostname: "testhost",
	}
	message := "ok\n<11>1 2026-01-01T00:00:00Z attacker opf - - - {\"fake\":true}\r\n\x1b[31mred\x00end"
	event := core.Evento{
		SessaoID:  7,
		Timestamp: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		Tipo:      core.EventoInfo,
		Mensagem:  message,
		Detalhes:  `{"note":"a\nb"}`,
	}

	line, err := sink.format(event)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
		t.Fatalf("formatted syslog message contains a physical line break: %q", line)
	}
	if strings.Contains(line, "\x00") || strings.Contains(line, "\x1b") {
		t.Fatalf("formatted syslog message contains a raw control character: %q", line)
	}
	if !strings.HasPrefix(line, "<14>1 2026-11-21T12:00:00Z testhost opf - - - ") {
		t.Fatalf("unexpected RFC 5424 header: %q", line)
	}

	const separator = " - - - "
	index := strings.Index(line, separator)
	if index < 0 {
		t.Fatalf("missing RFC 5424 structured data separator: %q", line)
	}
	payload := line[index+len(separator):]
	var decoded struct {
		SessionID int64  `json:"sessao_id"`
		Tipo      string `json:"tipo"`
		Mensagem  string `json:"mensagem"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%q)", err, payload)
	}
	if decoded.Mensagem != message {
		t.Fatalf("message was altered: got %q want %q", decoded.Mensagem, message)
	}
	if decoded.SessionID != event.SessaoID {
		t.Fatalf("session id = %d", decoded.SessionID)
	}
}

// TestSecuritySyslogTagPattern asserts the syslog tag whitelist blocks header
// injection characters.
func TestSecuritySyslogTagPattern(t *testing.T) {
	valid := []string{"opf", "opf-1.2", "OPF_test", "a"}
	for _, tag := range valid {
		if !tagPattern.MatchString(tag) {
			t.Errorf("valid tag %q rejected", tag)
		}
	}
	invalid := []string{"", " opf", "opf\nfake", "opf fake", "opf;id", "opf\x00", "-opf", "opf/tag"}
	for _, tag := range invalid {
		if tagPattern.MatchString(tag) {
			t.Errorf("invalid tag %q accepted", tag)
		}
	}
}
