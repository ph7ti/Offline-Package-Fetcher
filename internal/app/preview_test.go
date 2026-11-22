package app

import (
	"testing"

	"opf/internal/core"
)

func TestBuildPackagePreviewDetectsOnlyUpgrades(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportDefault,
		[]string{"vim"},
		[]core.CandidatePackage{
			{Name: "vim", Version: "9.0"},
			{Name: "curl", Version: "8.0"},
			{Name: "libc6", Version: "2.36", IsDependency: true},
		},
		[]core.InstalledPackage{
			{Name: "vim", Version: "8.2"},
			{Name: "libc6", Version: "2.35"},
		},
	)
	if len(data.Diff) != 2 {
		t.Fatalf("diff = %+v, esperava vim e libc6", data.Diff)
	}
	if !upgrades["vim"] || upgrades["libc6"] || upgrades["curl"] {
		t.Fatalf("upgrades = %+v", upgrades)
	}
	if len(data.Inventory) != 2 {
		t.Fatalf("inventário = %+v", data.Inventory)
	}
}

func TestBuildPackagePreviewArchQualifier(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportDefault,
		[]string{"libssl3:amd64"},
		[]core.CandidatePackage{{Name: "libssl3:amd64", Version: "3.0.2"}},
		[]core.InstalledPackage{{Name: "libssl3", Version: "3.0.1"}},
	)
	if len(data.Diff) != 1 || !upgrades["libssl3:amd64"] || !upgrades["libssl3"] {
		t.Fatalf("qualificador de arquitetura não normalizado: %+v / %+v", data.Diff, upgrades)
	}
}

func TestBuildPackagePreviewWithoutUpgrade(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportAll,
		[]string{"curl"},
		[]core.CandidatePackage{{Name: "curl", Version: "8.0"}},
		[]core.InstalledPackage{{Name: "vim", Version: "9.0"}},
	)
	if len(data.Diff) != 0 || len(upgrades) != 0 {
		t.Fatalf("novo pacote não deve gerar diff: %+v / %+v", data.Diff, upgrades)
	}
	if data.Mode != core.VersionReportAll || len(data.Inventory) != 1 {
		t.Fatalf("inventário all inválido: %+v", data)
	}
}
