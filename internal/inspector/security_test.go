package inspector

import (
	"testing"
)

// TestSecurityParseInstalledRejectsMalformedLines asserts inventory parsing only
// accepts structurally valid lines; extra fields, missing fields and non-installed
// entries are dropped rather than trusted.
func TestSecurityParseInstalledRejectsMalformedLines(t *testing.T) {
	aptOutput := "ii\tvim\t9.0\tamd64\n" +
		"ii\tcurl\n" + // missing fields
		"rc\tremoved\t1.0\tamd64\n" + // not installed
		"ii\tlibc6\t2.35\tamd64\textra\n" + // extra fields
		"\n" +
		"ii\tbash\t5.1\tamd64\n"
	installed, err := parseInstalled("apt", aptOutput)
	if err != nil {
		t.Fatalf("parseInstalled apt: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %+v, want only vim and bash", installed)
	}
	if installed[0].Name != "vim" || installed[1].Name != "bash" {
		t.Fatalf("installed = %+v", installed)
	}

	rpmOutput := "vim\t9.0\tx86_64\n" +
		"broken line\n" +
		"curl\t7.0\n"
	installed, err = parseInstalled("rpm", rpmOutput)
	if err != nil {
		t.Fatalf("parseInstalled rpm: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "vim" || installed[0].Arch != "x86_64" {
		t.Fatalf("installed = %+v", installed)
	}

	apkOutput := "vim-9.0.1-r0\n" +
		"not-a-version\n" +
		"libc6-1.2-r3\n"
	installed, err = parseInstalled("apk", apkOutput)
	if err != nil {
		t.Fatalf("parseInstalled apk: %v", err)
	}
	if len(installed) != 2 || installed[0].Name != "vim" || installed[1].Name != "libc6" {
		t.Fatalf("installed = %+v", installed)
	}
}

// TestSecurityParseInstalledTreatsNamesAsData documents that package names read
// from the authenticated target are stored as opaque data: no shell command is
// ever built from them (commands are built from validated request names only).
func TestSecurityParseInstalledTreatsNamesAsData(t *testing.T) {
	payload := "vim;id"
	installed, err := parseInstalled("apt", "ii\t"+payload+"\t9.0\tamd64\n")
	if err != nil {
		t.Fatalf("parseInstalled: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != payload {
		t.Fatalf("installed = %+v", installed)
	}
}

// TestSecuritySplitAPKVersionStructuralValidation asserts the apk version
// splitter rejects values without a recognisable version/revision structure.
// The numeric revision itself is not re-validated here; names and versions are
// treated as opaque data (see TestSecurityParseInstalledTreatsNamesAsData).
func TestSecuritySplitAPKVersionStructuralValidation(t *testing.T) {
	for _, value := range []string{"", "vim", "vim-r0", "vim-r"} {
		if name, version, err := splitAPKVersion(value); err == nil {
			t.Errorf("value %q accepted as %q %q", value, name, version)
		}
	}
	name, version, err := splitAPKVersion("vim-9.0.1-r0")
	if err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
	if name != "vim" || version != "9.0.1-r0" {
		t.Fatalf("split = %q %q", name, version)
	}
}
