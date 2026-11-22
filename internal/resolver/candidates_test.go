package resolver

import "testing"

func TestParseAptPolicy(t *testing.T) {
	out := `vim:
  Installed: 2:8.2.2434-3ubuntu1
  Candidate: 2:9.0.0-1
  Version table:
     2:9.0.0-1 500
curl:
  Installed: (none)
  Candidate: 8.5.0-1
`
	got := parseAptPolicy(out)
	if got["vim"] != "2:9.0.0-1" || got["curl"] != "8.5.0-1" {
		t.Fatalf("parseAptPolicy = %+v", got)
	}
}

func TestParseApkCandidates(t *testing.T) {
	out := `(1/2) Downloading vim-9.0.1-r0.apk
(2/2) Downloading libc-dev-0.7.2-r5.apk
`
	got, err := parseApkCandidates(out)
	if err != nil {
		t.Fatalf("parseApkCandidates: %v", err)
	}
	if got["vim"] != "9.0.1-r0" || got["libc-dev"] != "0.7.2-r5" {
		t.Fatalf("parseApkCandidates = %+v", got)
	}
}
