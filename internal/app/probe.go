package app

import (
	"context"
	"strings"
	"time"

	"opf/internal/core"
	"opf/internal/transfer"
)

// DetectRoot checks the remote account UID over SSH.
func DetectRoot(ctx context.Context, cfg core.SSHConfig) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager := transfer.NewSSHTransferManager()
	if err := manager.Connect(probeCtx, cfg); err != nil {
		return false, err
	}
	defer manager.Close()
	output, err := manager.RunCommand(probeCtx, "id -u")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "0", nil
}
