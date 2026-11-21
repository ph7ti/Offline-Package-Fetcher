package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("escrever %s: %v", name, err)
	}
	return path
}

func TestGerarVerificarManifesto(t *testing.T) {
	dir := t.TempDir()
	a := writeTestFile(t, dir, "vim_9.0_amd64.deb", "conteudo-vim")
	b := writeTestFile(t, dir, "libc6_2.35_amd64.deb", "conteudo-libc6")

	manifest, err := GerarManifesto(dir, []string{a, b})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	if manifest.Caminho != filepath.Join(dir, NomeManifesto) {
		t.Fatalf("caminho do manifesto = %q", manifest.Caminho)
	}
	if len(manifest.Arquivos) != 2 {
		t.Fatalf("entradas = %d, esperava 2", len(manifest.Arquivos))
	}
	if manifest.Arquivos[0].Nome != "libc6_2.35_amd64.deb" || manifest.Arquivos[1].Nome != "vim_9.0_amd64.deb" {
		t.Fatalf("manifesto não está ordenado: %+v", manifest.Arquivos)
	}
	hash, err := HashFile(a)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if manifest.Arquivos[1].SHA256 != hash {
		t.Fatalf("hash do manifesto = %s, esperava %s", manifest.Arquivos[1].SHA256, hash)
	}

	content, err := os.ReadFile(manifest.Caminho)
	if err != nil {
		t.Fatalf("ler manifesto: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("linhas do manifesto = %d, esperava 2", len(lines))
	}
	for _, line := range lines {
		if !strings.Contains(line, "  ") {
			t.Fatalf("formato do manifesto inválido: %q", line)
		}
	}

	verified, err := VerificarManifesto(manifest.Caminho, dir)
	if err != nil {
		t.Fatalf("VerificarManifesto: %v", err)
	}
	if len(verified.Arquivos) != 2 {
		t.Fatalf("entradas verificadas = %d", len(verified.Arquivos))
	}
}

func TestGerarManifestoArtefatoAusente(t *testing.T) {
	dir := t.TempDir()
	existing := writeTestFile(t, dir, "ok.deb", "ok")
	missing := filepath.Join(dir, "faltante.deb")

	_, err := GerarManifesto(dir, []string{existing, missing})
	if err == nil || !strings.Contains(err.Error(), "ausente") {
		t.Fatalf("esperava erro de artefato ausente: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, NomeManifesto)); !os.IsNotExist(statErr) {
		t.Fatalf("manifesto não deveria existir após falha")
	}
}

func TestGerarManifestoArtefatoVazio(t *testing.T) {
	dir := t.TempDir()
	empty := writeTestFile(t, dir, "vazio.deb", "")

	_, err := GerarManifesto(dir, []string{empty})
	if err == nil || !strings.Contains(err.Error(), "vazio") {
		t.Fatalf("esperava erro de artefato vazio: %v", err)
	}
}

func TestGerarManifestoDuplicado(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "dup.deb", "x")

	_, err := GerarManifesto(dir, []string{file, file})
	if err == nil || !strings.Contains(err.Error(), "duplicado") {
		t.Fatalf("esperava erro de duplicidade: %v", err)
	}
}

func TestVerificarManifestoArtefatoAusente(t *testing.T) {
	dir := t.TempDir()
	a := writeTestFile(t, dir, "a.deb", "a")
	b := writeTestFile(t, dir, "b.deb", "b")
	manifest, err := GerarManifesto(dir, []string{a, b})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	if err := os.Remove(b); err != nil {
		t.Fatalf("remover b: %v", err)
	}

	_, err = VerificarManifesto(manifest.Caminho, dir)
	if err == nil || !strings.Contains(err.Error(), "ausente") {
		t.Fatalf("esperava erro de artefato ausente: %v", err)
	}
}

func TestVerificarManifestoCorrompido(t *testing.T) {
	dir := t.TempDir()
	file := writeTestFile(t, dir, "pkg.deb", "original")
	manifest, err := GerarManifesto(dir, []string{file})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	if err := os.WriteFile(file, []byte("alterado"), 0o644); err != nil {
		t.Fatalf("corromper arquivo: %v", err)
	}

	_, err = VerificarManifesto(manifest.Caminho, dir)
	if err == nil || !strings.Contains(err.Error(), "divergente") {
		t.Fatalf("esperava erro de checksum divergente: %v", err)
	}
}

func TestVerificarManifestoInvalido(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		contains string
	}{
		{"linha vazia", "\n", "vazio"},
		{"linha malformada", "abc\n", "inválida"},
		{"hash inválido", "zzzz  a.deb\n", "hash"},
		{"caminho no nome", strings.Repeat("a", 64) + "  ../a.deb\n", "caminho"},
		{"duplicado", "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb  a.deb\n" +
			"ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb  a.deb\n", "duplicado"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, "a.deb", "a")
			manifestPath := filepath.Join(dir, NomeManifesto)
			if err := os.WriteFile(manifestPath, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("escrever manifesto: %v", err)
			}
			_, err := VerificarManifesto(manifestPath, dir)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("esperava erro contendo %q, obteve %v", tt.contains, err)
			}
		})
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "x.txt", "abc")
	hash, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if hash != expected {
		t.Fatalf("hash = %s, esperava %s", hash, expected)
	}
}
