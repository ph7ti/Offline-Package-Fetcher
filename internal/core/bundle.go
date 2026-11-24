package core

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"opf/internal/i18n"
	"opf/internal/transfer"
)

// OfflineBundleRequest holds the validated inputs needed to generate a
// self-contained air-gapped .tar.gz bundle (artifacts + SHA256SUMS +
// metadata.json + install-offline.sh).
type OfflineBundleRequest struct {
	OutputPath    string
	SessionID     int64
	TargetOS      string
	TargetVersion string
	TargetArch    string
	Family        string
	ManifestPath  string
	Artifacts     []PackageArtifact
	Requested     []string
}

type offlineBundleMetadata struct {
	SessionID         int64                  `json:"session_id"`
	CreatedAt         string                 `json:"created_at"`
	TargetOS          string                 `json:"target_os"`
	TargetVersion     string                 `json:"target_version"`
	TargetArch        string                 `json:"target_arch"`
	Family            string                 `json:"family"`
	RequestedPackages []string               `json:"requested_packages,omitempty"`
	ArtifactCount     int                    `json:"artifact_count"`
	Artifacts         []offlineBundlePackage `json:"artifacts"`
}

type offlineBundlePackage struct {
	File         string `json:"file"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	IsDependency bool   `json:"is_dependency"`
	SHA256       string `json:"sha256"`
}

// FamilyForTargetOS returns the package manager family ("apt", "rpm", "apk")
// for a normalized target distribution name.
func FamilyForTargetOS(targetOS string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(targetOS)) {
	case "ubuntu", "debian":
		return RepositoryFamilyAPT, nil
	case "centos", "rhel", "rockylinux", "rocky", "almalinux", "fedora":
		return RepositoryFamilyRPM, nil
	case "alpine":
		return RepositoryFamilyAPK, nil
	default:
		return "", fmt.Errorf(i18n.T("família de destino inválida para repositórios: %q"), targetOS)
	}
}

// GenerateOfflineBundle creates a self-contained .tar.gz archive for air-gapped
// (sneakernet) delivery containing the verified package artifacts, SHA256SUMS,
// non-secret session metadata, and an executable install-offline.sh script.
func GenerateOfflineBundle(req OfflineBundleRequest) (string, error) {
	outPath := strings.TrimSpace(req.OutputPath)
	if outPath == "" {
		return "", errors.New("caminho do bundle offline não informado")
	}
	if strings.ContainsAny(outPath, "\r\n\x00") {
		return "", fmt.Errorf("caminho do bundle offline inválido: %q", outPath)
	}
	if len(req.Artifacts) == 0 {
		return "", errors.New("nenhum artefato para gerar bundle offline")
	}
	if strings.TrimSpace(req.ManifestPath) == "" {
		return "", errors.New("manifesto SHA256SUMS não informado para o bundle offline")
	}
	family := strings.ToLower(strings.TrimSpace(req.Family))
	if family == "" {
		var err error
		family, err = FamilyForTargetOS(req.TargetOS)
		if err != nil {
			return "", err
		}
	}
	ext, err := extensionForFamily(family)
	if err != nil {
		return "", err
	}

	artifactNames := make([]string, 0, len(req.Artifacts))
	seen := make(map[string]struct{}, len(req.Artifacts))
	metaArtifacts := make([]offlineBundlePackage, 0, len(req.Artifacts))
	for _, a := range req.Artifacts {
		base := filepath.Base(a.LocalPath)
		if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) || strings.ContainsAny(base, "\r\n\x00") {
			return "", fmt.Errorf("nome de artefato inválido no bundle: %q", a.LocalPath)
		}
		if !strings.HasSuffix(base, ext) {
			return "", fmt.Errorf("artefato %q incompatível com família %s", base, family)
		}
		if _, dup := seen[base]; dup {
			return "", fmt.Errorf("artefato duplicado no bundle: %q", base)
		}
		seen[base] = struct{}{}
		artifactNames = append(artifactNames, base)
		metaArtifacts = append(metaArtifacts, offlineBundlePackage{
			File:         base,
			Name:         a.Name,
			Version:      a.Version,
			Arch:         a.Arch,
			IsDependency: a.IsDependency,
			SHA256:       a.SHA256,
		})
	}

	scriptText, err := BuildOfflineInstallScript(family, artifactNames)
	if err != nil {
		return "", err
	}

	meta := offlineBundleMetadata{
		SessionID:         req.SessionID,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		TargetOS:          req.TargetOS,
		TargetVersion:     req.TargetVersion,
		TargetArch:        req.TargetArch,
		Family:            family,
		RequestedPackages: append([]string(nil), req.Requested...),
		ArtifactCount:     len(req.Artifacts),
		Artifacts:         metaArtifacts,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serializar metadados do bundle: %w", err)
	}
	metaBytes = append(metaBytes, '\n')

	manifestBytes, err := os.ReadFile(req.ManifestPath)
	if err != nil {
		return "", fmt.Errorf("ler manifesto SHA256SUMS para o bundle: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("criar diretório do bundle offline: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".opf-bundle-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("criar arquivo temporário do bundle: %w", err)
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)

	rootDir := "opf-bundle"
	if req.SessionID > 0 {
		rootDir = fmt.Sprintf("opf-bundle-sessao-%d", req.SessionID)
	}
	now := time.Now().UTC()

	for _, a := range req.Artifacts {
		base := filepath.Base(a.LocalPath)
		if err := addFileToTar(tw, a.LocalPath, path.Join(rootDir, base), 0o644); err != nil {
			return "", fmt.Errorf("adicionar artefato %q ao bundle: %w", base, err)
		}
	}
	if err := addBytesToTar(tw, path.Join(rootDir, transfer.NomeManifesto), manifestBytes, 0o644, now); err != nil {
		return "", fmt.Errorf("adicionar SHA256SUMS ao bundle: %w", err)
	}
	if err := addBytesToTar(tw, path.Join(rootDir, "metadata.json"), metaBytes, 0o644, now); err != nil {
		return "", fmt.Errorf("adicionar metadata.json ao bundle: %w", err)
	}
	if err := addBytesToTar(tw, path.Join(rootDir, "install-offline.sh"), []byte(scriptText), 0o755, now); err != nil {
		return "", fmt.Errorf("adicionar install-offline.sh ao bundle: %w", err)
	}

	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("fechar tar do bundle: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("fechar gzip do bundle: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sincronizar bundle offline: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return "", fmt.Errorf("ajustar permissões do bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("fechar bundle offline: %w", err)
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return "", fmt.Errorf("publicar bundle offline: %w", err)
	}
	keep = true
	return outPath, nil
}

// BuildOfflineInstallScript generates the POSIX shell script embedded in an
// air-gapped bundle. Every artifact filename is POSIX single-quoted.
func BuildOfflineInstallScript(family string, artifactNames []string) (string, error) {
	if len(artifactNames) == 0 {
		return "", errors.New("nenhum artefato para gerar script offline")
	}
	quoted := make([]string, 0, len(artifactNames))
	for _, name := range artifactNames {
		if strings.ContainsAny(name, `/\`) || strings.ContainsAny(name, "\r\n\x00") || name == "" || name == "." || name == ".." {
			return "", fmt.Errorf("nome de artefato inválido para script offline: %q", name)
		}
		quoted = append(quoted, bundleShellQuote(name))
	}
	joined := strings.Join(quoted, " ")

	var installBlock string
	switch strings.ToLower(strings.TrimSpace(family)) {
	case RepositoryFamilyAPT:
		installBlock = fmt.Sprintf(
			"DEBIAN_FRONTEND=noninteractive dpkg -i %s || {\n"+
				"  DEBIAN_FRONTEND=noninteractive apt-get install -f -y --no-download\n"+
				"}\n",
			joined,
		)
	case RepositoryFamilyRPM:
		installBlock = fmt.Sprintf(
			"if command -v dnf >/dev/null 2>&1; then\n"+
				"  PM=dnf\n"+
				"elif command -v yum >/dev/null 2>&1; then\n"+
				"  PM=yum\n"+
				"else\n"+
				"  echo 'error: dnf/yum not found' >&2\n"+
				"  exit 1\n"+
				"fi\n"+
				"\"$PM\" localinstall -y --disablerepo='*' %s\n",
			joined,
		)
	case RepositoryFamilyAPK:
		installBlock = fmt.Sprintf("apk add --no-network --allow-untrusted %s\n", joined)
	default:
		return "", fmt.Errorf("família não suportada para script offline: %q", family)
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Offline Package Fetcher (OPF) — self-verifying air-gapped installer\n")
	b.WriteString("set -eu\n\n")
	b.WriteString("SCRIPT_DIR=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n")
	b.WriteString("cd \"$SCRIPT_DIR\"\n\n")
	b.WriteString("VERIFY_ONLY=0\n")
	b.WriteString("if [ \"${1:-}\" = \"--verify-only\" ]; then\n")
	b.WriteString("  VERIFY_ONLY=1\n")
	b.WriteString("fi\n\n")
	b.WriteString("echo \"==> Verifying SHA-256 checksums (P3)...\"\n")
	b.WriteString("sha256sum -c SHA256SUMS\n\n")
	b.WriteString("if [ \"$VERIFY_ONLY\" -eq 1 ]; then\n")
	b.WriteString("  echo \"OK: all artifacts verified.\"\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n\n")
	b.WriteString("if [ \"$(id -u)\" -ne 0 ]; then\n")
	b.WriteString("  echo \"error: root privileges required; run with sudo ./install-offline.sh\" >&2\n")
	b.WriteString("  exit 1\n")
	b.WriteString("fi\n\n")
	b.WriteString("echo \"==> Installing verified offline packages...\"\n")
	b.WriteString(installBlock)
	b.WriteString("echo \"OK: offline installation completed.\"\n")
	return b.String(), nil
}

func extensionForFamily(family string) (string, error) {
	switch family {
	case RepositoryFamilyAPT:
		return ".deb", nil
	case RepositoryFamilyRPM:
		return ".rpm", nil
	case RepositoryFamilyAPK:
		return ".apk", nil
	default:
		return "", fmt.Errorf("família inválida: %q", family)
	}
}

func addFileToTar(tw *tar.Writer, srcPath, tarPath string, mode int64) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("arquivo não é regular: %q", srcPath)
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr := &tar.Header{
		Name:    tarPath,
		Mode:    mode,
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func addBytesToTar(tw *tar.Writer, tarPath string, data []byte, mode int64, modTime time.Time) error {
	hdr := &tar.Header{
		Name:    tarPath,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: modTime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func bundleShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
