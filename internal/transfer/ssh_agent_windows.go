//go:build windows

package transfer

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

const windowsOpenSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

func dialSSHAgent() (net.Conn, error) {
	pipePath := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if pipePath == "" {
		pipePath = windowsOpenSSHAgentPipe
	}
	timeout := defaultSSHTimeout
	conn, err := winio.DialPipe(pipePath, &timeout)
	if err != nil {
		return nil, fmt.Errorf("conectar ao ssh-agent (%s): %w", pipePath, err)
	}
	return conn, nil
}
