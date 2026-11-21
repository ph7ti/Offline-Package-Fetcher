package app

import "testing"

func TestParseTargetPresets(t *testing.T) {
	tests := []struct {
		name, osRelease, arch                      string
		osName, version, family, image, targetArch string
		tested                                     bool
	}{
		{"ubuntu", "ID=ubuntu\nVERSION_ID=\"22.04\"\nPRETTY_NAME=\"Ubuntu 22.04\"\n", "x86_64", "ubuntu", "22.04", "apt", "ubuntu:22.04", "amd64", true},
		{"debian", "ID=debian\nVERSION_ID=\"12\"\n", "x86_64", "debian", "12", "apt", "debian:12", "amd64", true},
		{"alpine", "ID=alpine\nVERSION_ID=3.20\n", "x86_64", "alpine", "3.20", "apk", "alpine:3.20", "x86_64", true},
		{"rocky", "ID=\"rocky\"\nVERSION_ID=\"9.3\"\n", "x86_64", "rockylinux", "9.3", "rpm", "rockylinux:9.3", "x86_64", true},
		{"alma", "ID=almalinux\nVERSION_ID=\"9\"\n", "aarch64", "almalinux", "9", "rpm", "almalinux:9", "aarch64", true},
		{"centos7", "ID=centos\nVERSION_ID=\"7\"\n", "x86_64", "centos", "7", "rpm", "centos:7", "x86_64", true},
		{"centos stream", "ID=centos\nVERSION_ID=\"9\"\n", "x86_64", "centos", "9", "rpm", "quay.io/centos/centos:stream9", "x86_64", true},
		{"rhel", "ID=rhel\nVERSION_ID=\"9.2\"\n", "x86_64", "rhel", "9.2", "rpm", "registry.access.redhat.com/ubi9/ubi", "x86_64", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseTarget(tt.osRelease, tt.arch)
			if err != nil {
				t.Fatalf("ParseTarget: %v", err)
			}
			if info.OS != tt.osName || info.Version != tt.version || info.Family != tt.family || info.Image != tt.image || info.Arch != tt.targetArch || info.Tested != tt.tested {
				t.Fatalf("info = %+v", info)
			}
		})
	}
}

func TestParseTargetUnknown(t *testing.T) {
	if _, err := ParseTarget("ID=arch\nVERSION_ID=rolling\n", "x86_64"); err == nil {
		t.Fatal("esperava erro para distro desconhecida")
	}
}
