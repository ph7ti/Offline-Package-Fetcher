package observability

import (
	"strings"
	"testing"
	"time"

	"opf/internal/core"
	"opf/internal/i18n"
)

func TestMarkdownIncludesPackagePreview(t *testing.T) {
	i18n.Set(i18n.PTBR)
	r := &Relatorio{
		Sessao: core.Sessao{ID: 7, TargetOS: "ubuntu", TargetVersion: "22.04", TargetArch: "amd64", FetchMethod: "docker"},
		Preview: core.PackagePreviewData{
			Mode:      core.VersionReportDefault,
			Inventory: []core.InstalledPackage{{Name: "vim", Version: "8.2", Arch: "amd64"}},
			Diff:      []core.PackageDiff{{Name: "vim", From: "8.2", To: "9.0", IsDependency: false}},
			Decision:  "approved",
		},
		GeradoEm: time.Now().UTC(),
	}
	md := r.Markdown()
	for _, want := range []string{"## Package diff", "8.2", "9.0", "De pacote vim", "aprovado"} {
		if !strings.Contains(md, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, md)
		}
	}
}
