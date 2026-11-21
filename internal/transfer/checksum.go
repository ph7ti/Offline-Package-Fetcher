package transfer

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const NomeManifesto = "SHA256SUMS"

var hashPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type EntradaManifesto struct {
	Nome   string
	SHA256 string
}

type Manifesto struct {
	Caminho  string
	Arquivos []EntradaManifesto
}

// HashFile returns the lowercase SHA-256 hex digest of a regular file.
func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// GerarManifesto computes SHA-256 for every artifact, fails if any file is
// missing, empty or duplicated, and atomically writes <destDir>/SHA256SUMS.
func GerarManifesto(destDir string, arquivos []string) (Manifesto, error) {
	if destDir == "" {
		return Manifesto{}, errors.New("diretório do manifesto não informado")
	}
	if len(arquivos) == 0 {
		return Manifesto{}, errors.New("nenhum artefato para gerar o manifesto")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Manifesto{}, fmt.Errorf("criar diretório do manifesto: %w", err)
	}

	entries := make([]EntradaManifesto, 0, len(arquivos))
	seen := make(map[string]struct{}, len(arquivos))

	for _, arquivo := range arquivos {
		if arquivo == "" {
			return Manifesto{}, errors.New("caminho de artefato vazio")
		}
		nome := filepath.Base(arquivo)
		if err := validarNome(nome); err != nil {
			return Manifesto{}, err
		}
		if _, ok := seen[nome]; ok {
			return Manifesto{}, fmt.Errorf("artefato duplicado no manifesto: %q", nome)
		}
		seen[nome] = struct{}{}

		info, err := os.Stat(arquivo)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Manifesto{}, fmt.Errorf("artefato ausente: %q", nome)
			}
			return Manifesto{}, fmt.Errorf("inspecionar artefato %q: %w", nome, err)
		}
		if !info.Mode().IsRegular() {
			return Manifesto{}, fmt.Errorf("artefato não é arquivo regular: %q", nome)
		}
		if info.Size() == 0 {
			return Manifesto{}, fmt.Errorf("artefato vazio: %q", nome)
		}

		hash, err := HashFile(arquivo)
		if err != nil {
			return Manifesto{}, fmt.Errorf("calcular SHA-256 de %q: %w", nome, err)
		}
		entries = append(entries, EntradaManifesto{Nome: nome, SHA256: strings.ToLower(hash)})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Nome < entries[j].Nome })

	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(entry.SHA256)
		builder.WriteString("  ")
		builder.WriteString(entry.Nome)
		builder.WriteByte('\n')
	}

	manifestPath := filepath.Join(destDir, NomeManifesto)
	tmp, err := os.CreateTemp(destDir, ".SHA256SUMS.tmp-*")
	if err != nil {
		return Manifesto{}, fmt.Errorf("criar manifesto temporário: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return Manifesto{}, fmt.Errorf("ajustar permissões do manifesto: %w", err)
	}
	if _, err := tmp.WriteString(builder.String()); err != nil {
		tmp.Close()
		return Manifesto{}, fmt.Errorf("escrever manifesto: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Manifesto{}, fmt.Errorf("sincronizar manifesto: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Manifesto{}, fmt.Errorf("fechar manifesto: %w", err)
	}
	if err := os.Rename(tmpName, manifestPath); err != nil {
		return Manifesto{}, fmt.Errorf("publicar manifesto: %w", err)
	}

	return Manifesto{Caminho: manifestPath, Arquivos: entries}, nil
}

// VerificarManifesto validates every entry of a SHA256SUMS file against the
// files in dir. Missing files, malformed lines and hash mismatches are fatal.
func VerificarManifesto(manifestPath, dir string) (Manifesto, error) {
	if manifestPath == "" || dir == "" {
		return Manifesto{}, errors.New("caminho do manifesto ou diretório não informado")
	}

	file, err := os.Open(manifestPath)
	if err != nil {
		return Manifesto{}, fmt.Errorf("abrir manifesto: %w", err)
	}
	defer file.Close()

	entries := make([]EntradaManifesto, 0)
	seen := make(map[string]struct{})

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return Manifesto{}, fmt.Errorf("linha %d inválida no manifesto", lineNumber)
		}
		hash := fields[0]
		nome := fields[1]
		if !hashPattern.MatchString(hash) {
			return Manifesto{}, fmt.Errorf("linha %d com hash SHA-256 inválido", lineNumber)
		}
		if err := validarNome(nome); err != nil {
			return Manifesto{}, fmt.Errorf("linha %d: %w", lineNumber, err)
		}
		if _, ok := seen[nome]; ok {
			return Manifesto{}, fmt.Errorf("linha %d: artefato duplicado %q", lineNumber, nome)
		}
		seen[nome] = struct{}{}

		caminho := filepath.Join(dir, nome)
		info, err := os.Stat(caminho)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Manifesto{}, fmt.Errorf("artefato ausente: %q", nome)
			}
			return Manifesto{}, fmt.Errorf("inspecionar artefato %q: %w", nome, err)
		}
		if !info.Mode().IsRegular() {
			return Manifesto{}, fmt.Errorf("artefato não é arquivo regular: %q", nome)
		}
		if info.Size() == 0 {
			return Manifesto{}, fmt.Errorf("artefato vazio: %q", nome)
		}

		actual, err := HashFile(caminho)
		if err != nil {
			return Manifesto{}, fmt.Errorf("calcular SHA-256 de %q: %w", nome, err)
		}
		expected := strings.ToLower(hash)
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return Manifesto{}, fmt.Errorf("checksum divergente para %q", nome)
		}
		entries = append(entries, EntradaManifesto{Nome: nome, SHA256: expected})
	}
	if err := scanner.Err(); err != nil {
		return Manifesto{}, fmt.Errorf("ler manifesto: %w", err)
	}
	if len(entries) == 0 {
		return Manifesto{}, errors.New("manifesto vazio")
	}

	return Manifesto{Caminho: manifestPath, Arquivos: entries}, nil
}

func validarNome(nome string) error {
	if nome == "" || nome == "." || nome == ".." {
		return fmt.Errorf("nome de artefato inválido: %q", nome)
	}
	if filepath.Base(nome) != nome {
		return fmt.Errorf("nome de artefato contém caminho: %q", nome)
	}
	if strings.ContainsAny(nome, `/\`) {
		return fmt.Errorf("nome de artefato contém separador: %q", nome)
	}
	if strings.ContainsRune(nome, 0) {
		return fmt.Errorf("nome de artefato contém NUL: %q", nome)
	}
	return nil
}
