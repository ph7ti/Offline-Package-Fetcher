package i18n

import "testing"

func TestResolve(t *testing.T) {
	t.Setenv("OPF_LANG", "")
	cases := map[string]Language{
		"":      Detect(),
		"auto":  Detect(),
		"en":    EN,
		"pt-BR": PTBR,
		"es":    ES,
		"fr":    Detect(),
	}
	for setting, want := range cases {
		if got := Resolve(setting); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestResolveFromEnvironment(t *testing.T) {
	t.Setenv("OPF_LANG", "pt-BR")
	if got := Resolve("auto"); got != PTBR {
		t.Fatalf("Resolve(auto) with OPF_LANG=pt-BR = %q", got)
	}
	if got := Resolve("es"); got != ES {
		t.Fatalf("explicit setting must win over OPF_LANG, got %q", got)
	}
}

func TestTranslateAndFallback(t *testing.T) {
	previous := Current()
	defer Set(previous)

	Set(PTBR)
	if got := T("Nova sessão"); got != "Nova sessão" {
		t.Fatalf("pt-BR source changed: %q", got)
	}
	Set(EN)
	if got := T("string intentionally absent from catalogs"); got != "string intentionally absent from catalogs" {
		t.Fatalf("missing translation should fall back to source, got %q", got)
	}
}

func TestFormatting(t *testing.T) {
	previous := Current()
	defer Set(previous)
	Set(EN)
	got := T("Nova sessão %d", 7)
	if got != "Nova sessão 7" {
		t.Fatalf("format fallback = %q", got)
	}
}

func TestCatalogLoad(t *testing.T) {
	if err := CatalogError(); err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if len(CatalogLanguages()) == 0 {
		t.Fatal("no embedded catalogs found")
	}
}

func TestSupported(t *testing.T) {
	got := Supported()
	if len(got) != 3 || got[0] != EN || got[1] != PTBR || got[2] != ES {
		t.Fatalf("Supported() = %v", got)
	}
	for _, l := range got {
		if NativeName(l) == "" {
			t.Errorf("NativeName(%q) is empty", l)
		}
	}
}
