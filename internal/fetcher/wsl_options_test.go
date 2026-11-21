package fetcher

import "testing"

func TestDefaultWSLDistro(t *testing.T) {
	tests := map[string]string{
		"ubuntu:24.04": "Ubuntu-24.04",
		"ubuntu:22.04": "Ubuntu-22.04",
		"debian:12":    "Debian",
	}
	for input, want := range tests {
		osName, version, _ := cutTest(input)
		if got := defaultWSLDistro(osName, version); got != want {
			t.Errorf("defaultWSLDistro(%q) = %q, esperava %q", input, got, want)
		}
	}
}

func cutTest(value string) (string, string, bool) {
	for i := 0; i < len(value); i++ {
		if value[i] == ':' {
			return value[:i], value[i+1:], true
		}
	}
	return value, "", false
}
