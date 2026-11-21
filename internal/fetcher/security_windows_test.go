//go:build windows

package fetcher

import (
	"strings"
	"testing"
)

// TestSecurityWSLShellQuote asserts the WSL helper uses the same POSIX
// single-quote escape as the SSH/installer helpers.
func TestSecurityWSLShellQuote(t *testing.T) {
	tests := map[string]string{
		"/tmp/opf": "'/tmp/opf'",
		"a'b":      `'a'\''b'`,
		"a;id":     "'a;id'",
		"$(id)":    "'$(id)'",
		"`id`":     "'`id`'",
		"a b":      "'a b'",
		"":         "''",
	}
	for input, want := range tests {
		if got := wslShellQuote(input); got != want {
			t.Errorf("wslShellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestSecurityWSLShellJoin asserts every argument of a WSL command is quoted
// and that hostile arguments never appear outside single quotes.
func TestSecurityWSLShellJoin(t *testing.T) {
	payloads := []string{"a;id", "$(id)", "`id`", "a b", "a'b", "a|id", "a&&id", "${IFS}"}
	for _, payload := range payloads {
		joined := wslShellJoin([]string{"apk", "add", "--allow-untrusted", payload})
		state := quoteState(joined)
		offset := 0
		for offset <= len(joined)-len(payload) {
			index := strings.Index(joined[offset:], payload)
			if index < 0 {
				break
			}
			index += offset
			if !state[index] {
				t.Fatalf("payload %q escaped quoting in %q", payload, joined)
			}
			offset = index + 1
		}
		for _, arg := range []string{"apk", "add", "--allow-untrusted"} {
			if !strings.Contains(joined, "'"+arg+"'") {
				t.Fatalf("argument %q not quoted in %q", arg, joined)
			}
		}
	}
}

// quoteState reports whether each byte of command is inside single quotes.
func quoteState(command string) []bool {
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
