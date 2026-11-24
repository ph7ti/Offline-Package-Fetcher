//go:build !windows

package transfer

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

func dialSSHAgent() (net.Conn, error) {
	sock := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if sock == "" {
		return nil, errors.New("ssh-agent indisponível: variável SSH_AUTH_SOCK não definida")
	}
	conn, err := net.DialTimeout("unix", sock, defaultSSHTimeout)
	if err != nil {
		return nil, fmt.Errorf("conectar ao ssh-agent (%s): %w", sock, err)
	}
	return conn, nil
}
