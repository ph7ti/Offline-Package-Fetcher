package core

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"8.2.2434-3ubuntu1", "9.0-1", -1},
		{"9.0-1", "8.2.2434-3ubuntu1", 1},
		{"1.0-1", "1.0-1", 0},
		{"2:1.0-1", "1:9.9-9", 1},
		{"1.0~rc1-1", "1.0-1", -1},
		{"1.0-2", "1.0-10", -1},
		{"1.2.3-r0", "1.2.3-r1", -1},
		{"8.0.1-1.el9", "8.0.1-1.el9", 0},
		{"1.0a-1", "1.0+deb1-1", -1},
		{"1.0ubuntu1-1", "1.0+deb12u1-1", -1},
		{"1.0a-1", "1.0.1-1", -1},
		{"1.0-1", "1.0.1-1", -1},
		{"1.0~beta1-1", "1.0~beta2-1", -1},
		{"1.0~-1", "1.0~~-1", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseVersionReportMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want VersionReportMode
	}{
		{"", VersionReportDefault},
		{"all", VersionReportAll},
		{"default", VersionReportDefault},
		{"none", VersionReportNone},
	} {
		got, err := ParseVersionReportMode(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseVersionReportMode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseVersionReportMode("bad"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
