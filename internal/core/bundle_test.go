package core

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opf/internal/transfer"
)

func TestGenerateOfflineBundleAndScript(t *testing.T) {
	dir := t.TempDir()
	debA := filepath.Join(dir, "vim_9.0_amd64.deb")
	debB := filepath.Join(dir, "libc6_2.35_amd64.deb")
	if err := os.WriteFile(debA, []byte("vim-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(debB, []byte("libc-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := transfer.GerarManifesto(dir, []string{debA, debB})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}

	outBundle := filepath.Join(dir, "bundle.tar.gz")
	gotPath, err := GenerateOfflineBundle(OfflineBundleRequest{
		OutputPath:    outBundle,
		SessionID:     12,
		TargetOS:      "ubuntu",
		TargetVersion: "22.04",
		TargetArch:    "amd64",
		Family:        RepositoryFamilyAPT,
		ManifestPath:  manifest.Caminho,
		Requested:     []string{"vim"},
		Artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64", LocalPath: debA, SHA256: manifest.Arquivos[0].SHA256},
			{Name: "libc6", Version: "2.35", Arch: "amd64", LocalPath: debB, IsDependency: true, SHA256: manifest.Arquivos[1].SHA256},
		},
	})
	if err != nil {
		t.Fatalf("GenerateOfflineBundle: %v", err)
	}
	if gotPath != outBundle {
		t.Fatalf("gotPath = %q, want %q", gotPath, outBundle)
	}

	f, err := os.Open(outBundle)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	entries := map[string]string{}
	modes := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = string(data)
		modes[hdr.Name] = hdr.Mode
	}

	for _, name := range []string{
		"opf-bundle-sessao-12/vim_9.0_amd64.deb",
		"opf-bundle-sessao-12/libc6_2.35_amd64.deb",
		"opf-bundle-sessao-12/SHA256SUMS",
		"opf-bundle-sessao-12/metadata.json",
		"opf-bundle-sessao-12/install-offline.sh",
	} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("entrada ausente no bundle: %q (tem: %v)", name, entries)
		}
	}

	if modes["opf-bundle-sessao-12/install-offline.sh"] != 0o755 {
		t.Fatalf("modo do script = %o, esperava 0755", modes["opf-bundle-sessao-12/install-offline.sh"])
	}
	script := entries["opf-bundle-sessao-12/install-offline.sh"]
	for _, want := range []string{
		"sha256sum -c SHA256SUMS",
		"dpkg -i 'vim_9.0_amd64.deb' 'libc6_2.35_amd64.deb'",
		"apt-get install -f -y --no-download",
		"--verify-only",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script sem %q:\n%s", want, script)
		}
	}

	var meta offlineBundleMetadata
	if err := json.Unmarshal([]byte(entries["opf-bundle-sessao-12/metadata.json"]), &meta); err != nil {
		t.Fatalf("metadata.json inválido: %v", err)
	}
	if meta.SessionID != 12 || meta.ArtifactCount != 2 || meta.TargetOS != "ubuntu" {
		t.Fatalf("metadata inesperado: %+v", meta)
	}
}

func TestBuildOfflineInstallScriptFamiliesAndQuoting(t *testing.T) {
	rpmScript, err := BuildOfflineInstallScript(RepositoryFamilyRPM, []string{"vim;id.rpm"})
	if err != nil {
		t.Fatalf("RPM script: %v", err)
	}
	if !strings.Contains(rpmScript, "localinstall -y --disablerepo='*' 'vim;id.rpm'") {
		t.Fatalf("RPM script inesperado:\n%s", rpmScript)
	}

	apkScript, err := BuildOfflineInstallScript(RepositoryFamilyAPK, []string{"vim-9.1-r0.apk"})
	if err != nil {
		t.Fatalf("APK script: %v", err)
	}
	if !strings.Contains(apkScript, "apk add --no-network --allow-untrusted 'vim-9.1-r0.apk'") {
		t.Fatalf("APK script inesperado:\n%s", apkScript)
	}

	if _, err := BuildOfflineInstallScript(RepositoryFamilyAPT, []string{"../evil.deb"}); err == nil {
		t.Fatal("esperava erro para path traversal no script offline")
	}
}
