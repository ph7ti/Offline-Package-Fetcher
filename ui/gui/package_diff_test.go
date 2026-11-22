//go:build cgo

package gui

import (
	"testing"

	"fyne.io/fyne/v2/widget"

	"opf/internal/core"
	"opf/internal/i18n"
)

func TestVersionReportMappingRoundTrip(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)

	modes := []core.VersionReportMode{
		core.VersionReportAll,
		core.VersionReportDefault,
		core.VersionReportNone,
	}
	for _, lang := range []i18n.Language{i18n.PTBR, i18n.EN, i18n.ES} {
		i18n.Set(lang)
		for _, mode := range modes {
			label := versionReportLabel(mode)
			if got := versionReportModeFromLabel(label); got != mode {
				t.Errorf("[%s] versionReportModeFromLabel(%q) = %q, want %q", lang, label, got, mode)
			}
		}
	}
}

func TestSyncVersionReportForcesNoneWithoutSSH(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	ssh := widget.NewCheck("", nil)
	group := widget.NewRadioGroup(versionReportOptions(), nil)
	group.SetSelected(versionReportLabel(core.VersionReportDefault))
	u := &UI{sshEnabled: ssh, versionReportGroup: group}

	u.syncVersionReportSelect()
	if !group.Disabled() {
		t.Fatal("radio deveria ficar desabilitado sem SSH")
	}
	if got := group.Selected; got != versionReportLabel(core.VersionReportNone) {
		t.Fatalf("seleção = %q, want Nenhum", got)
	}
	if got := u.selectedVersionReportMode(); got != core.VersionReportNone {
		t.Fatalf("modo = %q, want none", got)
	}

	ssh.SetChecked(true)
	u.syncVersionReportSelect()
	if group.Disabled() {
		t.Fatal("radio deveria ficar habilitado com SSH")
	}
	if got := u.selectedVersionReportMode(); got != core.VersionReportDefault {
		t.Fatalf("modo ao reabilitar = %q, want default", got)
	}

	group.SetSelected(versionReportLabel(core.VersionReportAll))
	u.syncVersionReportSelect()
	if got := u.selectedVersionReportMode(); got != core.VersionReportAll {
		t.Fatalf("modo após escolher Todos = %q, want all", got)
	}
}

func TestDiffRoleLabel(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	if got := diffRoleLabel(core.PackageDiff{IsDependency: true}); got != "dependência" {
		t.Fatalf("papel dependência = %q", got)
	}
	if got := diffRoleLabel(core.PackageDiff{}); got != "principal" {
		t.Fatalf("papel principal = %q", got)
	}
}
