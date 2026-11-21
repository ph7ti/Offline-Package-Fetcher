package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"opf/internal/core"
	"opf/internal/i18n"
)

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type SyslogConfig struct {
	Enabled    bool
	Host       string
	Port       int
	Proto      string
	Tag        string
	Timeout    time.Duration
	QueueSize  int
	MaxRetries int
	Backoff    time.Duration
	Logger     *slog.Logger
}

// SyslogSink sends events asynchronously over RFC 5424 UDP/TCP. SQLite remains
// the synchronous source of truth; queue overflow or remote failure is
// best-effort and never blocks the pipeline.
type SyslogSink struct {
	cfg       SyslogConfig
	logger    *slog.Logger
	hostname  string
	events    chan core.Evento
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

func NewSyslogSink(cfg SyslogConfig) (*SyslogSink, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	cfg.Proto = strings.ToLower(strings.TrimSpace(cfg.Proto))
	if cfg.Proto == "" {
		cfg.Proto = "udp"
	}
	if cfg.Proto != "udp" && cfg.Proto != "tcp" {
		return nil, fmt.Errorf(i18n.T("protocolo syslog inválido: %q"), cfg.Proto)
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New(i18n.T("host syslog não informado"))
	}
	if cfg.Port == 0 {
		cfg.Port = 514
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf(i18n.T("porta syslog inválida: %d"), cfg.Port)
	}
	if cfg.Tag == "" {
		cfg.Tag = "opf"
	}
	if !tagPattern.MatchString(cfg.Tag) {
		return nil, fmt.Errorf(i18n.T("tag syslog inválida: %q"), cfg.Tag)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 512
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 200 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "-"
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &SyslogSink{
		cfg: cfg, logger: cfg.Logger, hostname: hostname,
		events: make(chan core.Evento, cfg.QueueSize),
		done:   make(chan struct{}),
		ctx:    ctx, cancel: cancel,
	}
	go sink.run()
	return sink, nil
}

func (s *SyslogSink) Emit(_ context.Context, event core.Evento) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return errors.New(i18n.T("syslog sink já fechado"))
	}
	select {
	case s.events <- event:
		return nil
	default:
		return errors.New(i18n.T("fila do syslog cheia"))
	}
}

func (s *SyslogSink) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.events)
		s.mu.Unlock()
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		s.cancel()
		<-s.done
		return ctx.Err()
	}
}

func (s *SyslogSink) run() {
	defer close(s.done)
	for event := range s.events {
		s.sendWithRetry(event)
	}
}

func (s *SyslogSink) sendWithRetry(event core.Evento) {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var lastErr error
	for attempt := 1; attempt <= s.cfg.MaxRetries; attempt++ {
		if err := s.send(event); err == nil {
			return
		} else {
			lastErr = err
		}
		if attempt == s.cfg.MaxRetries {
			break
		}
		delay := s.cfg.Backoff * time.Duration(1<<uint(attempt-1))
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	s.logger.Warn("syslog indisponível após retries; evento permanece no SQLite",
		"target", address,
		"tipo", event.Tipo,
		"erro", lastErr,
	)
}

func (s *SyslogSink) send(event core.Evento) error {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := net.DialTimeout(s.cfg.Proto, address, s.cfg.Timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(s.cfg.Timeout)); err != nil {
		return err
	}
	line, err := s.format(event)
	if err != nil {
		return err
	}
	if s.cfg.Proto == "tcp" {
		line += "\n"
	}
	_, err = conn.Write([]byte(line))
	return err
}

func (s *SyslogSink) format(event core.Evento) (string, error) {
	payload := map[string]any{
		"sessao_id": event.SessaoID,
		"tipo":      event.Tipo,
		"mensagem":  event.Mensagem,
	}
	if event.Detalhes != "" {
		payload["detalhes"] = json.RawMessage(event.Detalhes)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	priority := 14
	if event.Tipo == core.EventoErro {
		priority = 11
	}
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	return fmt.Sprintf("<%d>1 %s %s %s - - - %s",
		priority,
		timestamp.UTC().Format(time.RFC3339Nano),
		s.hostname,
		s.cfg.Tag,
		string(data),
	), nil
}
