package tui

import (
	"os"
	"testing"

	"opf/internal/i18n"
)

// TestMain pins the interface language to Brazilian Portuguese so tests keep
// asserting the historical source strings regardless of the host locale.
func TestMain(m *testing.M) {
	_ = os.Setenv("OPF_LANG", string(i18n.PTBR))
	i18n.Set(i18n.PTBR)
	code := m.Run()
	os.Exit(code)
}
