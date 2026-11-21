package resolver

import (
	"testing"
)

// TestSecurityAptCacheOutputRejection asserts hostile or unexpected apt-cache
// lines abort resolution instead of being treated as package names.
func TestSecurityAptCacheOutputRejection(t *testing.T) {
	invalid := []string{
		"vim;id",
		"$(id)",
		"`id`",
		"vim && id",
		"vim|id",
		"vim\nlibc6\nevil;id",
		"../etc/passwd",
		"Depends: vim",
	}
	for _, output := range invalid {
		output := output
		if deps, err := parseAptCacheDepends(output); err == nil {
			t.Errorf("output %q accepted: %v", output, deps)
		}
	}

	valid := "vim\n  Depends: libc6\n<debconf-2.0>\nW: warning\nlibc6\n"
	deps, err := parseAptCacheDepends(valid)
	if err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
	if len(deps) != 2 || deps[0] != "libc6" || deps[1] != "vim" {
		t.Fatalf("deps = %v", deps)
	}
}

// TestSecurityRepoqueryOutputRejection asserts repoquery output is filtered to
// the package-name whitelist; injection lines are dropped, and output with no
// valid line is rejected.
func TestSecurityRepoqueryOutputRejection(t *testing.T) {
	deps, err := parseRepoquery("vim\nlibc6\nvim;id\n$(id)\n`id`\nname with space\n")
	if err != nil {
		t.Fatalf("parseRepoquery: %v", err)
	}
	for _, dep := range deps {
		if !rpmPackagePattern.MatchString(dep) {
			t.Fatalf("invalid package name %q accepted", dep)
		}
	}
	if len(deps) != 2 || !contains(deps, "vim") || !contains(deps, "libc6") {
		t.Fatalf("deps = %v", deps)
	}
	if _, err := parseRepoquery("vim;id\n$(id)\n"); err == nil {
		t.Fatal("output without a valid package accepted")
	}
}

// TestSecurityApkSimulateOutputRejection asserts apk fetch simulation output
// cannot smuggle traversal or shell payloads into the dependency list.
func TestSecurityApkSimulateOutputRejection(t *testing.T) {
	invalid := []string{
		"Downloading vim;id-9.0-r0.apk",
		"Downloading $(id)-9.0-r0.apk",
		"Downloading ../evil-9.0-r0.apk",
		"Downloading vim-9.0.apk",
		"Downloading not-a-package",
	}
	for _, output := range invalid {
		if deps, err := parseApkSimulate(output); err == nil {
			t.Errorf("output %q accepted: %v", output, deps)
		}
	}

	deps, err := parseApkSimulate("Downloading vim-9.0.1-r0.apk\nDownloading libc6-1.2-r3.apk\n")
	if err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
	if len(deps) != 2 || !contains(deps, "vim") || !contains(deps, "libc6") {
		t.Fatalf("deps = %v", deps)
	}
}

// TestSecurityAptPackagePattern asserts package names cannot contain shell or
// path metacharacters.
func TestSecurityAptPackagePattern(t *testing.T) {
	resolver := NewAptResolver(nil, "amd64", "/tmp/opf")
	valid := []string{"vim", "libc6", "python3.10", "g++", "libc6-dev", "vim:amd64"}
	for _, name := range valid {
		if err := resolver.validatePackage(name); err != nil {
			t.Errorf("valid package %q rejected: %v", name, err)
		}
	}
	invalid := []string{
		"", "Vim", "vim;id", "$(id)", "`id`", "vim id", "vim\nid", "vim\x00id",
		"../etc/passwd", "vim|id", "vim&&id", "-v", ".", "..", "vim:id:amd64",
	}
	for _, name := range invalid {
		if err := resolver.validatePackage(name); err == nil {
			t.Errorf("invalid package %q accepted", name)
		}
	}
}
