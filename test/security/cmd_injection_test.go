package security

import (
	"context"
	"strings"
	"testing"

	"opf/internal/core"
	"opf/internal/installer"
)

// injectionPayloads are filename fragments an attacker controls through a
// malicious package name or a hostile repository artifact. They contain no
// path separator, NUL, CR or LF: those are rejected outright and covered by
// TestInstallerRejectsUnsafeArtifactNames.
var injectionPayloads = []string{
	"x;id",
	"$(id)",
	"`id`",
	"a'b",
	"a|id",
	"a&id",
	"a>out",
	"a<in",
	"a b",
	"a\tb",
	"a; touch pwned",
	"a$(touch pwned)",
	"a`id`b",
	"${IFS}",
	"-R",
	"--force-all",
	"a'b;id",
}

// TestInstallerCommandInjection proves that every installer neutralises shell
// metacharacters in artifact filenames. The generated command must embed the
// POSIX single-quoted token, and no payload may appear outside single quotes.
func TestInstallerCommandInjection(t *testing.T) {
	const remoteDir = "/tmp/opf-security-suite"

	for family, executor := range installersFor(t) {
		family, executor := family, executor
		t.Run(family, func(t *testing.T) {
			if err := executor.SetRemoteDir(remoteDir); err != nil {
				t.Fatalf("SetRemoteDir: %v", err)
			}
			suffix := artifactSuffix(family)
			for _, payload := range injectionPayloads {
				name := payload + suffix
				command, err := executor.InstallCommand([]core.PackageArtifact{{
					Name: payload, LocalPath: "/tmp/downloads/" + name,
				}})
				if err != nil {
					// Path-bearing payloads that end up invalid are rejected,
					// never executed; report them as rejected, not failures.
					continue
				}
				if !strings.Contains(command, singleQuote(name)) {
					t.Fatalf("artifact %q is not shell-quoted in: %s", name, command)
				}
				assertOnlyQuoted(t, command, name)
				if !strings.Contains(command, singleQuote(remoteDir)) {
					t.Fatalf("remote dir %q is not shell-quoted in: %s", remoteDir, command)
				}
			}
		})
	}
}

// TestInstallerRemoteDirInjection covers the other attacker-influenced string
// concatenated into the install script: the remote session directory.
func TestInstallerRemoteDirInjection(t *testing.T) {
	for family := range installersFor(t) {
		family := family
		t.Run(family, func(t *testing.T) {
			for _, dir := range []string{
				"/tmp/opf;id",
				"/tmp/opf$(id)",
				"/tmp/opf`id`",
				"/tmp/opf a",
				"/tmp/opf'quote",
			} {
				executor := installersFor(t)[family]
				if err := executor.SetRemoteDir(dir); err != nil {
					t.Fatalf("SetRemoteDir(%q): %v", dir, err)
				}
				command, err := executor.InstallCommand([]core.PackageArtifact{{
					LocalPath: "/tmp/vim" + artifactSuffix(family),
				}})
				if err != nil {
					t.Fatalf("InstallCommand(%q): %v", dir, err)
				}
				if !strings.Contains(command, singleQuote(dir)) {
					t.Fatalf("remote dir %q is not shell-quoted in: %s", dir, command)
				}
				assertOnlyQuoted(t, command, "id")
			}
		})
	}
}

// TestInstallerRejectsUnsafeArtifactNames asserts that names that cannot be
// safely quoted or that could escape the download directory are rejected.
func TestInstallerRejectsUnsafeArtifactNames(t *testing.T) {
	for family, executor := range installersFor(t) {
		family, executor := family, executor
		t.Run(family, func(t *testing.T) {
			if err := executor.SetRemoteDir("/tmp/opf"); err != nil {
				t.Fatalf("SetRemoteDir: %v", err)
			}
			suffix := artifactSuffix(family)
			cases := map[string][]core.PackageArtifact{
				"empty path":        {{LocalPath: ""}},
				"newline in name":   {{LocalPath: "/tmp/a\nb" + suffix}},
				"carriage return":   {{LocalPath: "/tmp/a\rb" + suffix}},
				"NUL byte":          {{LocalPath: "/tmp/a\x00b" + suffix}},
				"path as name only": {{LocalPath: "/"}},
				"wrong suffix":      {{LocalPath: "/tmp/a.deb"}},
				"duplicate": {
					{LocalPath: "/tmp/a" + suffix},
					{LocalPath: "/other/a" + suffix},
				},
				"empty list": nil,
			}
			if family == "apt" {
				delete(cases, "wrong suffix")
				cases["wrong suffix"] = []core.PackageArtifact{{LocalPath: "/tmp/a.rpm"}}
			}
			if family == "rpm" {
				cases["wrong suffix"] = []core.PackageArtifact{{LocalPath: "/tmp/a.deb"}}
			}
			if family == "apk" {
				cases["wrong suffix"] = []core.PackageArtifact{{LocalPath: "/tmp/a.deb"}}
			}
			for name, artifacts := range cases {
				if _, err := executor.InstallCommand(artifacts); err == nil {
					t.Errorf("%s: expected rejection for %s", family, name)
				}
			}
		})
	}
}

// TestSudoPasswordNeverInCommand asserts the sudo password is not persisted in
// the generated command and that unusable passwords are rejected.
func TestSudoPasswordNeverInCommand(t *testing.T) {
	const password = "dummy-S3cret';$(id)`id`"
	for family, executor := range installersFor(t, installer.WithSudoPassword(password)) {
		family, executor := family, executor
		t.Run(family, func(t *testing.T) {
			if err := executor.SetRemoteDir("/tmp/opf"); err != nil {
				t.Fatalf("SetRemoteDir: %v", err)
			}
			command, err := executor.InstallCommand([]core.PackageArtifact{{
				LocalPath: "/tmp/vim" + artifactSuffix(family),
			}})
			if err != nil {
				t.Fatalf("InstallCommand: %v", err)
			}
			if strings.Contains(command, password) {
				t.Fatalf("sudo password leaked into command: %s", command)
			}
			if strings.Contains(command, "S3cret") {
				t.Fatalf("sudo password fragment leaked into command: %s", command)
			}
			if !strings.Contains(command, "sudo -S -p '' sh -c ") {
				t.Fatalf("sudo is not using stdin authentication: %s", command)
			}
		})
	}
}

// TestSudoPasswordRejectsControlCharacters asserts a password with CR/LF or NUL
// can never be injected into sudo's stdin stream.
func TestSudoPasswordRejectsControlCharacters(t *testing.T) {
	for _, password := range []string{"a\nb", "a\rb", "a\x00b"} {
		for family, executor := range installersFor(t, installer.WithSudoPassword(password)) {
			if err := executor.SetRemoteDir("/tmp/opf"); err != nil {
				t.Fatalf("SetRemoteDir: %v", err)
			}
			if _, err := executor.InstallCommand([]core.PackageArtifact{{
				LocalPath: "/tmp/vim" + artifactSuffix(family),
			}}); err == nil {
				t.Errorf("%s: password %q should be rejected", family, password)
			}
		}
	}
}

// TestSudoPasswordGoesThroughStdin asserts the password reaches the transfer
// manager through stdin, not through the command line.
func TestSudoPasswordGoesThroughStdin(t *testing.T) {
	const password = "dummy-password"
	for family, executor := range installersFor(t, installer.WithSudoPassword(password)) {
		family, executor := family, executor
		t.Run(family, func(t *testing.T) {
			if err := executor.SetRemoteDir("/tmp/opf"); err != nil {
				t.Fatalf("SetRemoteDir: %v", err)
			}
			command, err := executor.InstallCommand([]core.PackageArtifact{{
				LocalPath: "/tmp/vim" + artifactSuffix(family),
			}})
			if err != nil {
				t.Fatalf("InstallCommand: %v", err)
			}
			transfer := &fakeTransferManager{output: "ok"}
			if err := executor.Execute(context.Background(), transfer, command); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if string(transfer.input) != password+"\n" {
				t.Fatalf("stdin = %q, want password only", transfer.input)
			}
			if strings.Contains(transfer.commands[len(transfer.commands)-1], password) {
				t.Fatal("password present in executed command")
			}
		})
	}
}
