package version

import (
	"strings"
	"testing"
	"time"
)

func TestCurrentHasReleaseMetadata(t *testing.T) {
	info := Current()
	if info.Version == "" || info.ReleaseDate == "" || info.Commit == "" {
		t.Fatalf("metadados incompletos: %+v", info)
	}
	if _, err := time.Parse("2006-01-02", info.ReleaseDate); err != nil {
		t.Fatalf("data de lançamento inválida: %q", info.ReleaseDate)
	}
	if len(info.Commit) > 12 {
		t.Fatalf("commit não truncado: %q", info.Commit)
	}
	if strings.ContainsAny(info.Commit, " \t\n") {
		t.Fatalf("commit inválido: %q", info.Commit)
	}
}
