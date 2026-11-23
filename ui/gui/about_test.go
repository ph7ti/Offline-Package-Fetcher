//go:build cgo

package gui

import (
	"testing"

	"opf/internal/i18n"
	"opf/internal/version"
)

func TestAboutDetails(t *testing.T) {
	previous := i18n.Current()
	defer i18n.Set(previous)
	i18n.Set(i18n.PTBR)

	details := aboutDetails()
	want := map[string]string{
		"Nome":              "Offline Package Fetcher (OPF)",
		"Versão":            version.Current().Version,
		"Build / commit":    version.Current().Commit,
		"Data de lançamento": version.Current().ReleaseDate,
		"Desenvolvedor":     "Raphael Pereira",
		"Licença":           "GPL-3.0",
	}
	if len(details) != len(want) {
		t.Fatalf("aboutDetails = %d campos, esperava %d", len(details), len(want))
	}
	for _, detail := range details {
		if expected, ok := want[detail.Label]; !ok || detail.Value != expected {
			t.Fatalf("campo %q = %q, esperava %q", detail.Label, detail.Value, expected)
		}
	}
}
