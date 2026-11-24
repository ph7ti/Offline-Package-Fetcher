// Package version centralizes the application version and build metadata.
package version

import (
	"runtime/debug"
	"strings"
)

// These values are the release defaults. Release builds may override them with
// -ldflags "-X opf/internal/version.Version=... -X opf/internal/version.Commit=...".
var (
	Version     = "0.4.0"
	Commit      = ""
	ReleaseDate = "2026-11-23"
)

// Info is a snapshot of the build metadata shown in the About dialog.
type Info struct {
	Version     string
	Commit      string
	ReleaseDate string
}

// Current returns the build metadata, using the embedded VCS revision when the
// linker did not provide an explicit commit.
func Current() Info {
	commit := strings.TrimSpace(Commit)
	if commit == "" {
		commit = vcsRevision()
	}
	if commit == "" {
		commit = "dev"
	}
	return Info{
		Version:     strings.TrimSpace(Version),
		Commit:      shortCommit(commit),
		ReleaseDate: strings.TrimSpace(ReleaseDate),
	}
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
