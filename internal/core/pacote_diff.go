package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// VersionReportMode controls how installed package versions are collected for
// the Package diff screen and the report.
type VersionReportMode string

const (
	VersionReportAll     VersionReportMode = "all"
	VersionReportDefault VersionReportMode = "default"
	VersionReportNone    VersionReportMode = "none"
)

// ParseVersionReportMode validates a --version-report value.
func ParseVersionReportMode(value string) (VersionReportMode, error) {
	switch VersionReportMode(strings.TrimSpace(value)) {
	case VersionReportAll:
		return VersionReportAll, nil
	case VersionReportDefault, "":
		return VersionReportDefault, nil
	case VersionReportNone:
		return VersionReportNone, nil
	default:
		return "", fmt.Errorf("modo de coleta inválido: %q (use all, default ou none)", value)
	}
}

// CandidatePackage is a package version available in the target repositories.
type CandidatePackage struct {
	Name         string `json:"nome"`
	Version      string `json:"versao"`
	IsDependency bool   `json:"eh_dependencia"`
}

// PackageDiff is an upgrade discovered between an installed version and the
// candidate version that will be installed.
type PackageDiff struct {
	Name         string `json:"nome"`
	From         string `json:"de"`
	To           string `json:"para"`
	IsDependency bool   `json:"eh_dependencia"`
}

// PackagePreviewData is persisted with the session so reports and history can
// show the collected inventory, the upgrade diff and the user's decision.
type PackagePreviewData struct {
	Mode      VersionReportMode  `json:"modo"`
	Inventory []InstalledPackage `json:"inventario,omitempty"`
	Diff      []PackageDiff      `json:"diff,omitempty"`
	Decision  string             `json:"decisao,omitempty"` // approved | aborted
}

// CandidateResolver is implemented by dependency resolvers that can read
// candidate repository versions without downloading the packages.
type CandidateResolver interface {
	ListCandidates(ctx context.Context, pkgs []string, includeOptional bool) ([]CandidatePackage, error)
}

// PackageVersionResolver is implemented by fetchers that can resolve candidate
// versions inside their isolated environment.
type PackageVersionResolver interface {
	ResolveCandidates(ctx context.Context, roots []string, includeOptional bool) ([]CandidatePackage, error)
}

// InstalledPackageLister is implemented by inspectors that can list every
// installed package on the destination.
type InstalledPackageLister interface {
	ListInstalled(ctx context.Context) ([]InstalledPackage, error)
}

// CompareVersions compares two package versions using dpkg/RPM-like ordering.
// It returns -1 when a < b, 0 when a == b and 1 when a > b.
func CompareVersions(a, b string) int {
	if cmp := compareEpoch(a, b); cmp != 0 {
		return cmp
	}
	_, aRest := splitEpoch(a)
	_, bRest := splitEpoch(b)
	aUp, aRev := splitRevision(aRest)
	bUp, bRev := splitRevision(bRest)
	if cmp := compareVersionPart(aUp, bUp); cmp != 0 {
		return cmp
	}
	return compareVersionPart(aRev, bRev)
}

func splitEpoch(v string) (string, string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return "0", v
}

func compareEpoch(a, b string) int {
	ae, _ := splitEpoch(a)
	be, _ := splitEpoch(b)
	return compareNumeric(ae, be)
}

func splitRevision(v string) (string, string) {
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func compareNumeric(a, b string) int {
	ai, _ := strconv.Atoi(strings.TrimLeft(a, "0"))
	bi, _ := strconv.Atoi(strings.TrimLeft(b, "0"))
	switch {
	case ai < bi:
		return -1
	case ai > bi:
		return 1
	default:
		return 0
	}
}

func compareVersionPart(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// A tilde sorts before everything, including the end of the string.
		if i < len(a) && a[i] == '~' {
			if j < len(b) && b[j] == '~' {
				i++
				j++
				continue
			}
			return -1
		}
		if j < len(b) && b[j] == '~' {
			return 1
		}
		// Non-digit segments compare lexically (letters before end).
		if (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			var ca, cb byte
			if i < len(a) && !isDigit(a[i]) {
				ca = a[i]
				i++
			}
			if j < len(b) && !isDigit(b[j]) {
				cb = b[j]
				j++
			}
			if ca != cb {
				if ca < cb {
					return -1
				}
				return 1
			}
			continue
		}
		// Numeric segments compare by value.
		si, sj := i, j
		for i < len(a) && isDigit(a[i]) {
			i++
		}
		for j < len(b) && isDigit(b[j]) {
			j++
		}
		na := strings.TrimLeft(a[si:i], "0")
		nb := strings.TrimLeft(b[sj:j], "0")
		if len(na) != len(nb) {
			if len(na) < len(nb) {
				return -1
			}
			return 1
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
