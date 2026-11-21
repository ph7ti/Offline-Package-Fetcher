package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDatabasePathIsWritableUserPath(t *testing.T) {
	path, err := DefaultDatabasePath()
	if err != nil {
		t.Fatalf("DefaultDatabasePath: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("DefaultDatabasePath = %q, want an absolute path", path)
	}
	if filepath.Base(path) != "opf.db" {
		t.Fatalf("DefaultDatabasePath = %q, want .../opf/opf.db", path)
	}
	if filepath.Base(filepath.Dir(path)) != "opf" {
		t.Fatalf("DefaultDatabasePath = %q, want parent directory named opf", path)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	if filepath.Dir(filepath.Dir(path)) != configDir {
		t.Fatalf("DefaultDatabasePath = %q, want under %q", path, configDir)
	}
}

func TestDefaultOutputDirIsUserPath(t *testing.T) {
	path, err := DefaultOutputDir()
	if err != nil {
		t.Fatalf("DefaultOutputDir: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("DefaultOutputDir = %q, want an absolute path", path)
	}
	if filepath.Base(path) != "OPF" {
		t.Fatalf("DefaultOutputDir = %q, want .../OPF", path)
	}
}
