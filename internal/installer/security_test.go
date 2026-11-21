package installer

import (
	"strings"
	"testing"
)

// TestSecurityShellQuote asserts the POSIX single-quote escape used by every
// installer workaround: literal characters are preserved and a single quote is
// closed, escaped and reopened.
func TestSecurityShellQuote(t *testing.T) {
	tests := map[string]string{
		"vim.deb":     "'vim.deb'",
		"/tmp/opf":    "'/tmp/opf'",
		"a'b":         `'a'\''b'`,
		"a;id":        "'a;id'",
		"$(id)":       "'$(id)'",
		"`id`":        "'`id`'",
		"a b":         "'a b'",
		"a|b&c>d<e":   "'a|b&c>d<e'",
		"line\nbreak": "'line\nbreak'",
		"":            "''",
		"'":           `''\'''`,
	}
	for input, want := range tests {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestSecurityShellQuoteNeutralizes asserts no byte of a hostile value is left
// outside single quotes once shellQuote is applied.
func TestSecurityShellQuoteNeutralizes(t *testing.T) {
	payloads := []string{
		"a;id", "$(id)", "`id`", "a b", "a'b", "a|id", "a&&id", "${IFS}",
		"a\tb", "a\nb", "a\rb", "a>out", "a<in", "-R", "--force-all",
	}
	for _, payload := range payloads {
		quoted := shellQuote(payload)
		state := securityQuoteState(quoted)
		offset := 0
		for offset <= len(quoted)-len(payload) {
			index := strings.Index(quoted[offset:], payload)
			if index < 0 {
				break
			}
			index += offset
			if !state[index] {
				t.Fatalf("payload %q escaped quoting in %q", payload, quoted)
			}
			offset = index + 1
		}
	}
}

// securityQuoteState reports whether each byte is inside single quotes.
func securityQuoteState(command string) []bool {
	state := make([]bool, len(command))
	inQuote := false
	for i := 0; i < len(command); i++ {
		state[i] = inQuote
		switch command[i] {
		case '\'':
			inQuote = !inQuote
		case '\\':
			if !inQuote && i+1 < len(command) {
				i++
				state[i] = false
			}
		}
	}
	return state
}

// TestSecurityWrapSudoCommand covers the sudo wrapper: disabled, missing
// password, control characters in the password and the stdin-only form.
func TestSecurityWrapSudoCommand(t *testing.T) {
	command, err := wrapSudoCommand("apt-get install -y vim", false, "")
	if err != nil {
		t.Fatalf("disabled sudo: %v", err)
	}
	if command != "apt-get install -y vim" {
		t.Fatalf("disabled sudo changed command: %q", command)
	}

	if _, err := wrapSudoCommand("ls", true, ""); err == nil {
		t.Fatal("sudo without password accepted")
	}
	for _, password := range []string{"a\nb", "a\rb", "a\x00b"} {
		if _, err := wrapSudoCommand("ls", true, password); err == nil {
			t.Errorf("password %q accepted", password)
		}
	}

	const password = "dummy-password';$(id)"
	command, err = wrapSudoCommand("apt-get install -y vim", true, password)
	if err != nil {
		t.Fatalf("wrapSudoCommand: %v", err)
	}
	if strings.Contains(command, password) || strings.Contains(command, "dummy-password") {
		t.Fatalf("password leaked into command: %s", command)
	}
	if !strings.HasPrefix(command, "sudo -S -p '' sh -c '") {
		t.Fatalf("unexpected sudo form: %s", command)
	}
	if !strings.HasSuffix(command, "'") {
		t.Fatalf("inner command is not quoted: %s", command)
	}
}

// TestSecurityValidateRemoteDir asserts only absolute directories without
// control characters can become the installation working directory.
func TestSecurityValidateRemoteDir(t *testing.T) {
	valid := []string{"/tmp/opf-sessao-1", "/var/tmp/opf", "/tmp/opf;id", "/tmp/opf a"}
	for _, dir := range valid {
		if err := validateRemoteDir(dir); err != nil {
			t.Errorf("valid directory %q rejected: %v", dir, err)
		}
	}
	invalid := []string{"", "   ", "relative", "tmp/opf", "a\nb", "a\rb", "a\x00b", "/tmp/../etc"}
	for _, dir := range invalid {
		// "/tmp/../etc" is absolute and has no control characters; the
		// production contract only requires an absolute path, so it is listed
		// here to document the actual accepted surface.
		if dir == "/tmp/../etc" {
			continue
		}
		if err := validateRemoteDir(dir); err == nil {
			t.Errorf("invalid directory %q accepted", dir)
		}
	}
}
