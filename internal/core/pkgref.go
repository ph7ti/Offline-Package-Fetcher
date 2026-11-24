package core

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	aptPkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	aptPkgArchRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	aptPkgVerRe  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+:~-]*$`)

	rpmPkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*$`)
	rpmPkgVerRe  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_:~-]*$`)

	apkPkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*$`)
	apkPkgVerRe  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)
)

// PackageSpec represents a validated package request, with optional architecture
// qualifier and optional pinned version (e.g. nginx=1.18.0-6ubuntu14.4).
type PackageSpec struct {
	Raw     string
	Name    string
	Arch    string
	Version string
}

// ParsePackageSpec validates and parses a package specification for the given
// family ("apt", "rpm", "apk"). Version pinning uses '=' (e.g. pkg=version, or
// pkg:arch=version on APT); RPM also accepts NEVRA forms like pkg-1.2.3-1.
func ParsePackageSpec(family, raw string) (PackageSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PackageSpec{}, fmt.Errorf("nome de pacote vazio")
	}
	if strings.ContainsAny(raw, " \t\r\n\x00;|&$`<>'\"\\/") || strings.Contains(raw, "..") {
		return PackageSpec{}, fmt.Errorf("nome de pacote inválido: %q", raw)
	}

	left, ver, hasVer := strings.Cut(raw, "=")
	if hasVer && ver == "" {
		return PackageSpec{}, fmt.Errorf("versão ausente na especificação do pacote: %q", raw)
	}

	switch strings.ToLower(strings.TrimSpace(family)) {
	case RepositoryFamilyAPT:
		name, arch, hasArch := strings.Cut(left, ":")
		if hasArch && !aptPkgArchRe.MatchString(arch) {
			return PackageSpec{}, fmt.Errorf("arquitetura inválida no pacote APT: %q", raw)
		}
		if !aptPkgNameRe.MatchString(name) {
			return PackageSpec{}, fmt.Errorf("nome de pacote APT inválido: %q", raw)
		}
		if hasVer && !aptPkgVerRe.MatchString(ver) {
			return PackageSpec{}, fmt.Errorf("versão inválida no pacote APT: %q", raw)
		}
		return PackageSpec{Raw: raw, Name: name, Arch: arch, Version: ver}, nil

	case RepositoryFamilyRPM:
		if !rpmPkgNameRe.MatchString(left) {
			return PackageSpec{}, fmt.Errorf("nome de pacote RPM inválido: %q", raw)
		}
		if hasVer && !rpmPkgVerRe.MatchString(ver) {
			return PackageSpec{}, fmt.Errorf("versão inválida no pacote RPM: %q", raw)
		}
		return PackageSpec{Raw: raw, Name: left, Version: ver}, nil

	case RepositoryFamilyAPK:
		if !apkPkgNameRe.MatchString(left) {
			return PackageSpec{}, fmt.Errorf("nome de pacote APK inválido: %q", raw)
		}
		if hasVer && !apkPkgVerRe.MatchString(ver) {
			return PackageSpec{}, fmt.Errorf("versão inválida no pacote APK: %q", raw)
		}
		return PackageSpec{Raw: raw, Name: left, Version: ver}, nil

	default:
		return PackageSpec{}, fmt.Errorf("família de pacote inválida: %q", family)
	}
}

// PackageBaseName extracts the bare package name without '=version' or ':arch'.
func PackageBaseName(spec string) string {
	spec = strings.TrimSpace(spec)
	if idx := strings.IndexByte(spec, '='); idx >= 0 {
		spec = spec[:idx]
	}
	if idx := strings.IndexByte(spec, ':'); idx >= 0 {
		spec = spec[:idx]
	}
	return spec
}

// PackagePinnedVersion returns the pinned version from a 'name=version' spec,
// or empty string when unpinned.
func PackagePinnedVersion(spec string) string {
	if _, ver, ok := strings.Cut(strings.TrimSpace(spec), "="); ok {
		return ver
	}
	return ""
}
