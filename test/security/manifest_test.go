package security

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/transfer"
)

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func writeManifest(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, transfer.NomeManifesto)
	writeFile(t, path, content)
	return path
}

// TestManifestTamperDetected asserts any modification to an artifact after
// manifest generation is fatal, including same-size rewrites.
func TestManifestTamperDetected(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.deb")
	b := filepath.Join(dir, "b.deb")
	writeFile(t, a, "package-a-content")
	writeFile(t, b, "package-b-content")

	manifest, err := transfer.GerarManifesto(dir, []string{a, b})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	if _, err := transfer.VerificarManifesto(manifest.Caminho, dir); err != nil {
		t.Fatalf("fresh manifest rejected: %v", err)
	}

	// Same length, different content: only a real hash comparison catches it.
	writeFile(t, a, strings.Repeat("X", len("package-a-content")))
	if _, err := transfer.VerificarManifesto(manifest.Caminho, dir); err == nil ||
		!strings.Contains(err.Error(), "checksum divergente") {
		t.Fatalf("tampered artifact not detected: %v", err)
	}

	// Restore and truncate: size change must also be detected.
	writeFile(t, a, "package-a-content")
	writeFile(t, b, "")
	if _, err := transfer.VerificarManifesto(manifest.Caminho, dir); err == nil ||
		!strings.Contains(err.Error(), "vazio") {
		t.Fatalf("emptied artifact not detected: %v", err)
	}
}

// TestManifestRejectsTraversalAndMalformedEntries asserts a manifest cannot
// reference files outside the artifact directory or smuggle shell/SQL text.
func TestManifestRejectsTraversalAndMalformedEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "vim.deb"), "vim")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir sub: %v", err)
	}
	hash := sha256Hex("vim")

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"parent traversal", hash + "  ../evil\n", "caminho"},
		{"absolute path", hash + "  /etc/passwd\n", "caminho"},
		{"nested path", hash + "  sub/file.deb\n", "caminho"},
		{"duplicate", hash + "  vim.deb\n" + hash + "  vim.deb\n", "duplicado"},
		{"malformed hash", "zz  vim.deb\n", "hash SHA-256 inválido"},
		{"too many fields", hash + "  vim.deb extra\n", "linha 1 inválida"},
		{"space in name", hash + "  my file.deb\n", "linha 1 inválida"},
		{"empty", "\n\n", "manifesto vazio"},
		{"missing artifact", hash + "  missing.deb\n", "ausente"},
		{"directory entry", hash + "  sub\n", "não é arquivo regular"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := writeManifest(t, dir, tt.content)
			_, err := transfer.VerificarManifesto(manifestPath, dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestManifestRejectsSymlinkTraversal asserts a symlink cannot be used to make
// the verifier read a non-regular file outside the artifact directory.
func TestManifestRejectsSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("Mkdir outside: %v", err)
	}
	link := filepath.Join(dir, "link.deb")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	manifestPath := writeManifest(t, dir, sha256Hex("x")+"  link.deb\n")
	_, err := transfer.VerificarManifesto(manifestPath, dir)
	if err == nil || !strings.Contains(err.Error(), "não é arquivo regular") {
		t.Fatalf("symlink to directory accepted: %v", err)
	}
}

// TestManifestEntriesUseBasename proves artifact inventory cannot inject a path
// into the manifest: only filepath.Base is written, and duplicate basenames are
// rejected instead of overwriting each other.
func TestManifestEntriesUseBasename(t *testing.T) {
	dest := t.TempDir()
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirA, "x.deb"), "a")
	writeFile(t, filepath.Join(dirB, "x.deb"), "b")

	if _, err := transfer.GerarManifesto(dest, []string{
		filepath.Join(dirA, "x.deb"),
		filepath.Join(dirB, "x.deb"),
	}); err == nil || !strings.Contains(err.Error(), "duplicado") {
		t.Fatalf("duplicate basenames accepted: %v", err)
	}

	manifest, err := transfer.GerarManifesto(dest, []string{filepath.Join(dirA, "x.deb")})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	data, err := os.ReadFile(manifest.Caminho)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(data), dirA) || !strings.Contains(string(data), "  x.deb\n") {
		t.Fatalf("manifest contains a path instead of a basename: %s", data)
	}
}

// TestGerarManifestoRejectsUnsafeArtifacts asserts non-regular, empty and
// missing artifacts are rejected before a manifest is published.
func TestGerarManifestoRejectsUnsafeArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty.deb"), "")
	writeFile(t, filepath.Join(dir, "vim.deb"), "vim")
	if err := os.Mkdir(filepath.Join(dir, "dir.deb"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	tests := map[string]string{
		"empty":     "vazio",
		"directory": "não é arquivo regular",
		"missing":   "ausente",
	}
	paths := map[string]string{
		"empty":     filepath.Join(dir, "empty.deb"),
		"directory": filepath.Join(dir, "dir.deb"),
		"missing":   filepath.Join(dir, "missing.deb"),
	}
	for name, wantErr := range tests {
		if _, err := transfer.GerarManifesto(t.TempDir(), []string{paths[name]}); err == nil ||
			!strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: error = %v, want %q", name, err, wantErr)
		}
	}
}

// TestManifestComparisonUsesConstantTime is a regression guard: the verifier
// compares digests with crypto/subtle so a digest mismatch does not leak
// position information. Behavioural detection of the mismatch itself is covered
// by TestManifestTamperDetected.
func TestManifestComparisonUsesConstantTime(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "internal", "transfer", "checksum.go"))
	if err != nil {
		t.Fatalf("read checksum.go: %v", err)
	}
	if !strings.Contains(string(source), "subtle.ConstantTimeCompare") {
		t.Fatal("manifest verification no longer uses subtle.ConstantTimeCompare")
	}
}
