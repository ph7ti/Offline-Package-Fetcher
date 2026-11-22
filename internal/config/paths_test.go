package config

import (
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
	if filepath.Base(filepath.Dir(path)) != "data" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "OPF" {
		t.Fatalf("DefaultDatabasePath = %q, want .../OPF/data/opf.db", path)
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
