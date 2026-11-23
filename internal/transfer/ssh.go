package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultSSHPort       = 22
	defaultSSHTimeout    = 15 * time.Second
	sshCopyBufferSize    = 64 * 1024
	maxUploadConcurrency = 4
)

// SSHConfig groups SSH connection parameters, including optional jump hosts.
type SSHConfig struct {
	Host       string
	Port       int
	User       string
	KeyPath    string
	Password   string
	KnownHosts string
	// InsecureSkipHostKey disables SSH host key verification. It is an explicit
	// opt-in and equivalent to StrictHostKeyChecking=no.
	InsecureSkipHostKey bool
	// JumpHosts are dialed in order, each one through the previous connection,
	// before reaching the destination. Nested jumps are rejected.
	JumpHosts []SSHConfig
}

// TransferManager handles SSH/SFTP transfer and remote verification.
type TransferManager interface {
	Connect(ctx context.Context, cfg SSHConfig) error
	Upload(ctx context.Context, files []string, remoteDir string) error
	VerifyChecksums(ctx context.Context, manifestPath, remoteDir string) error
	RunCommand(ctx context.Context, cmd string) (stdout string, err error)
	Close() error
}

// SSHTransferManager implements TransferManager using SSH and SFTP.
// Jump hosts are dialed in order through the previous SSH connection.
type ProgressFunc func(stage string, current, total int64)

type SSHTransferManager struct {
	jumps    []*ssh.Client
	client   *ssh.Client
	sftp     *sftp.Client
	progress ProgressFunc
}

var _ TransferManager = (*SSHTransferManager)(nil)

func NewSSHTransferManager() *SSHTransferManager {
	return &SSHTransferManager{}
}

func (m *SSHTransferManager) SetProgress(progress ProgressFunc) {
	m.progress = progress
}

// IsConnected reports whether an SSH and SFTP session is currently active.
func (m *SSHTransferManager) IsConnected() bool {
	return m.client != nil && m.sftp != nil
}

func (m *SSHTransferManager) Connect(ctx context.Context, cfg SSHConfig) error {
	if m.client != nil {
		return errors.New("transferência SSH já conectada")
	}
	if err := validateSSHConfig(cfg, "destino"); err != nil {
		return err
	}
	for i, jump := range cfg.JumpHosts {
		role := fmt.Sprintf("jump %d", i+1)
		if len(jump.JumpHosts) > 0 {
			return fmt.Errorf("%s não pode ter jumps aninhados", role)
		}
		if err := validateSSHConfig(jump, role); err != nil {
			return err
		}
	}

	var (
		jumps []*ssh.Client
		via   *ssh.Client
	)
	for i, jump := range cfg.JumpHosts {
		client, err := dialSSH(ctx, jump, via)
		if err != nil {
			closeSSHClients(jumps)
			return fmt.Errorf("conectar ao jump %d (%s): %w", i+1, jump.Host, err)
		}
		jumps = append(jumps, client)
		via = client
	}

	client, err := dialSSH(ctx, cfg, via)
	if err != nil {
		closeSSHClients(jumps)
		return fmt.Errorf("conectar ao destino %s: %w", cfg.Host, err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		closeSSHClients(jumps)
		return fmt.Errorf("abrir SFTP em %s: %w", cfg.Host, err)
	}

	m.jumps = jumps
	m.client = client
	m.sftp = sftpClient
	return nil
}

func (m *SSHTransferManager) Upload(ctx context.Context, files []string, remoteDir string) error {
	if m.sftp == nil {
		return errors.New("transferência SSH não conectada")
	}
	if len(files) == 0 {
		return errors.New("nenhum arquivo para enviar")
	}
	if err := validateRemotePath(remoteDir); err != nil {
		return err
	}
	stopContextWatch := context.AfterFunc(ctx, func() { _ = m.sftp.Close() })
	defer stopContextWatch()
	if err := m.sftp.MkdirAll(remoteDir); err != nil {
		return fmt.Errorf("criar diretório remoto %q: %w", remoteDir, err)
	}

	type uploadJob struct {
		localPath  string
		remotePath string
		name       string
	}
	var total int64
	seen := make(map[string]struct{}, len(files))
	jobs := make([]uploadJob, 0, len(files))
	for _, localPath := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(localPath)
		if err != nil {
			return fmt.Errorf("inspecionar arquivo local %q: %w", localPath, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("arquivo local não é regular: %q", localPath)
		}
		name := filepath.Base(localPath)
		if err := validateRemotePath(name); err != nil {
			return fmt.Errorf("nome local inválido %q: %w", localPath, err)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("arquivo duplicado no envio: %q", name)
		}
		seen[name] = struct{}{}
		total += info.Size()
		jobs = append(jobs, uploadJob{
			localPath:  localPath,
			remotePath: path.Join(remoteDir, name),
			name:       name,
		})
	}

	workers := maxUploadConcurrency
	if len(jobs) < workers {
		workers = len(jobs)
	}

	uploadCtx, cancelUpload := context.WithCancel(ctx)
	defer cancelUpload()

	var (
		written    atomic.Int64
		progressMu sync.Mutex
		errOnce    sync.Once
		firstErr   error
	)
	recordErr := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancelUpload()
		})
	}
	onWritten := func(n int64) {
		cur := written.Add(n)
		if m.progress != nil {
			progressMu.Lock()
			m.progress("upload", cur, total)
			progressMu.Unlock()
		}
	}

	jobCh := make(chan uploadJob, len(jobs))
	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if uploadCtx.Err() != nil {
					return
				}
				if err := uploadFile(uploadCtx, m.sftp, job.localPath, job.remotePath, onWritten); err != nil {
					recordErr(fmt.Errorf("enviar %q: %w", job.name, err))
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func (m *SSHTransferManager) VerifyChecksums(ctx context.Context, manifestPath, remoteDir string) error {
	if m.client == nil {
		return errors.New("transferência SSH não conectada")
	}
	if err := validateRemotePath(remoteDir); err != nil {
		return err
	}
	if err := validateRemotePath(manifestPath); err != nil {
		return err
	}
	manifestName := path.Base(path.Clean(manifestPath))
	if manifestName == "." || manifestName == ".." || manifestName == "/" {
		return fmt.Errorf("manifesto remoto inválido: %q", manifestPath)
	}
	if err := validateRemotePath(manifestName); err != nil {
		return err
	}

	cmd := fmt.Sprintf("cd %s && sha256sum -c %s", shellQuote(remoteDir), shellQuote(manifestName))
	output, err := m.RunCommand(ctx, cmd)
	if err != nil {
		return fmt.Errorf("verificar checksums no destino (%s): %w", remoteDir, err)
	}
	_ = output
	return nil
}

func (m *SSHTransferManager) RunCommand(ctx context.Context, cmd string) (string, error) {
	return m.runCommand(ctx, cmd, nil)
}

// RunCommandWithInput executes a remote command with stdin. It is used by
// sudo -S so the sudo password never appears in the command line.
func (m *SSHTransferManager) RunCommandWithInput(ctx context.Context, cmd string, input []byte) (string, error) {
	return m.runCommand(ctx, cmd, input)
}

func (m *SSHTransferManager) runCommand(ctx context.Context, cmd string, input []byte) (string, error) {
	if m.client == nil {
		return "", errors.New("transferência SSH não conectada")
	}
	if strings.TrimSpace(cmd) == "" {
		return "", errors.New("comando remoto vazio")
	}

	session, err := m.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("abrir sessão SSH: %w", err)
	}
	defer session.Close()
	if input != nil {
		session.Stdin = bytes.NewReader(input)
	}

	type commandResult struct {
		output []byte
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		output, err := session.CombinedOutput(cmd)
		done <- commandResult{output: output, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		result := <-done
		if ctx.Err() != nil {
			return string(result.output), ctx.Err()
		}
		return string(result.output), classifySSHError(result.err, result.output)
	case result := <-done:
		return string(result.output), classifySSHError(result.err, result.output)
	}
}

func (m *SSHTransferManager) RunCommandStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	return m.runCommandStream(ctx, cmd, nil, stdout, stderr)
}

// RunCommandStreamWithInput streams remote command stdout/stderr while feeding
// stdin (used by sudo -S with live logs without exposing the password in argv).
func (m *SSHTransferManager) RunCommandStreamWithInput(ctx context.Context, cmd string, input []byte, stdout, stderr io.Writer) error {
	return m.runCommandStream(ctx, cmd, input, stdout, stderr)
}

func (m *SSHTransferManager) runCommandStream(ctx context.Context, cmd string, input []byte, stdout, stderr io.Writer) error {
	if m.client == nil {
		return errors.New("transferência SSH não conectada")
	}
	if strings.TrimSpace(cmd) == "" {
		return errors.New("comando remoto vazio")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	session, err := m.client.NewSession()
	if err != nil {
		return fmt.Errorf("abrir sessão SSH: %w", err)
	}
	defer session.Close()
	if input != nil {
		session.Stdin = bytes.NewReader(input)
	}
	session.Stdout = stdout
	session.Stderr = stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		return classifySSHError(err, nil)
	}
}

func (m *SSHTransferManager) RemoveRemoteDir(ctx context.Context, dir string) error {
	if m.client == nil {
		return errors.New("transferência SSH não conectada")
	}
	dir = path.Clean(strings.TrimSpace(dir))
	if !strings.HasPrefix(dir, "/tmp/opf-") && !strings.HasPrefix(dir, "/var/tmp/opf-") {
		return fmt.Errorf("diretório remoto fora da área de cleanup: %q", dir)
	}
	output, err := m.RunCommand(ctx, "rm -rf -- "+shellQuote(dir))
	if err != nil {
		return fmt.Errorf("remover diretório remoto %q: %w", dir, err)
	}
	_ = output
	return nil
}

func (m *SSHTransferManager) Close() error {
	var errs []error
	if m.sftp != nil {
		if err := m.sftp.Close(); err != nil {
			errs = append(errs, fmt.Errorf("fechar SFTP: %w", err))
		}
		m.sftp = nil
	}
	if m.client != nil {
		if err := m.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("fechar SSH do destino: %w", err))
		}
		m.client = nil
	}
	for i := len(m.jumps) - 1; i >= 0; i-- {
		if err := m.jumps[i].Close(); err != nil {
			errs = append(errs, fmt.Errorf("fechar SSH do jump %d: %w", i+1, err))
		}
	}
	m.jumps = nil
	return errors.Join(errs...)
}

func dialSSH(ctx context.Context, cfg SSHConfig, via *ssh.Client) (*ssh.Client, error) {
	addr := sshAddress(cfg.Host, cfg.Port)
	clientConfig, err := buildSSHClientConfig(cfg)
	if err != nil {
		return nil, err
	}

	var conn net.Conn
	if via == nil {
		dialer := net.Dialer{Timeout: defaultSSHTimeout}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = via.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("abrir TCP para %s: %w", addr, err)
	}

	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()

	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	close(done)
	<-watcherDone
	if err != nil {
		conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("handshake SSH com %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(clientConn, chans, reqs), nil
}

func buildSSHClientConfig(cfg SSHConfig) (*ssh.ClientConfig, error) {
	var hostKeyCallback ssh.HostKeyCallback
	knownHostsPath := ""
	if cfg.InsecureSkipHostKey {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		var err error
		knownHostsPath, err = resolveKnownHosts(cfg.KnownHosts)
		if err != nil {
			return nil, err
		}
		knownHosts, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("carregar known_hosts %q: %w", knownHostsPath, err)
		}
		hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if err := knownHosts(hostname, remote, key); err != nil {
				return fmt.Errorf("host key de %s não confere com %s: %w", hostname, knownHostsPath, err)
			}
			return nil
		}
	}

	authMethods, err := sshAuthMethods(cfg)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:    cfg.User,
		Auth:    authMethods,
		Timeout: defaultSSHTimeout,
		// Prefer ed25519, like OpenSSH, so a known_hosts generated by the
		// user's ssh client still matches the negotiated host key.
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA256,
			ssh.KeyAlgoRSASHA512,
			ssh.KeyAlgoRSA,
		},
		HostKeyCallback: hostKeyCallback,
	}, nil
}

func sshAuthMethods(cfg SSHConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if cfg.KeyPath != "" {
		keyPath, err := expandHome(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("resolver chave SSH: %w", err)
		}
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ler chave SSH %q: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("interpretar chave SSH %q: %w", keyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}
	if len(methods) == 0 {
		return nil, errors.New("nenhuma autenticação SSH configurada (chave ou senha)")
	}
	return methods, nil
}

func resolveKnownHosts(value string) (string, error) {
	pathValue := value
	if pathValue == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolver known_hosts padrão: %w", err)
		}
		pathValue = filepath.Join(home, ".ssh", "known_hosts")
	}
	resolved, err := expandHome(pathValue)
	if err != nil {
		return "", fmt.Errorf("resolver known_hosts: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("known_hosts %q indisponível: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("known_hosts %q não é arquivo regular", resolved)
	}
	return resolved, nil
}

func validateSSHConfig(cfg SSHConfig, role string) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return fmt.Errorf("%s sem host SSH", role)
	}
	if strings.TrimSpace(cfg.User) == "" {
		return fmt.Errorf("%s sem usuário SSH", role)
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("%s com porta SSH inválida: %d", role, cfg.Port)
	}
	if strings.ContainsRune(cfg.Host, 0) || strings.ContainsAny(cfg.Host, "\r\n") {
		return fmt.Errorf("%s com host SSH inválido", role)
	}
	if cfg.KeyPath == "" && cfg.Password == "" {
		return fmt.Errorf("%s sem chave nem senha SSH", role)
	}
	return nil
}

func sshAddress(host string, port int) string {
	if port == 0 {
		port = defaultSSHPort
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func expandHome(value string) (string, error) {
	if value == "" {
		return "", errors.New("caminho vazio")
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if value == "~" {
			return home, nil
		}
		return filepath.Join(home, value[2:]), nil
	}
	return value, nil
}

func uploadFile(ctx context.Context, client *sftp.Client, localPath, remotePath string, onWritten func(int64)) (err error) {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			_ = client.Remove(remotePath)
		}
	}()

	writer := io.Writer(dst)
	if onWritten != nil {
		writer = &progressWriter{dst: dst, onWrite: onWritten}
	}
	if _, err := copyWithContext(ctx, writer, src); err != nil {
		return err
	}
	if err := dst.Chmod(0o644); err != nil {
		return err
	}
	return nil
}

type progressWriter struct {
	dst     io.Writer
	onWrite func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.onWrite(int64(n))
	}
	return n, err
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, sshCopyBufferSize)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := src.Read(buffer)
		if read > 0 {
			count, writeErr := dst.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func validateRemotePath(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("caminho remoto vazio")
	}
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("caminho remoto inválido: %q", value)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func classifySSHError(err error, output []byte) error {
	if err == nil {
		return nil
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("comando remoto saiu com status %d: %s",
			exitErr.ExitStatus(), truncateOutput(string(output), 4096))
	}
	return err
}

func truncateOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "... (truncado)"
}

func closeSSHClients(clients []*ssh.Client) {
	for i := len(clients) - 1; i >= 0; i-- {
		_ = clients[i].Close()
	}
}
