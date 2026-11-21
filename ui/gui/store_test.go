//go:build cgo

package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestOpenStoreFallsBackToUserDatabase(t *testing.T) {
	test.NewApp()
	temp := t.TempDir()
	t.Setenv("APPDATA", temp)         // Windows
	t.Setenv("XDG_CONFIG_HOME", temp) // Linux/macOS

	// A regular file where a directory is expected makes the configured
	// SQLite path unopenable, emulating a read-only or removed location.
	blocked := filepath.Join(temp, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	u := &UI{dbEntry: widget.NewEntry()}
	u.dbEntry.SetText(filepath.Join(blocked, "opf.db"))
	u.openStore()
	if u.store == nil {
		t.Fatalf("openStore returned no store; settingsErr=%v", u.settingsErr)
	}
	t.Cleanup(func() { _ = u.store.Close() })

	want := filepath.Join(temp, "opf", "opf.db")
	if filepath.Clean(u.dbEntry.Text) != want {
		t.Fatalf("dbEntry.Text = %q, want fallback %q", u.dbEntry.Text, want)
	}
	if u.settingsErr == nil || !strings.Contains(u.settingsErr.Error(), want) {
		t.Fatalf("settingsErr = %v, want a warning mentioning %q", u.settingsErr, want)
	}
}
