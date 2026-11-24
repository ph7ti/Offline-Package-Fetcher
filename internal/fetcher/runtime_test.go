package fetcher

import (
	"strings"
	"testing"
)

func TestResolveContainerHostExplicitAndEnv(t *testing.T) {
	got, err := ResolveContainerHost("podman", "unix:///tmp/podman.sock")
	if err != nil || got != "unix:///tmp/podman.sock" {
		t.Fatalf("explicit = %q, %v", got, err)
	}
	if _, err := ResolveContainerHost("podman", "unix:///tmp/bad\nsock"); err == nil {
		t.Fatal("esperava erro para host com newline")
	}

	t.Setenv("CONTAINER_HOST", "unix:///run/user/1000/podman/podman.sock")
	got, err = ResolveContainerHost("podman", "")
	if err != nil || got != "unix:///run/user/1000/podman/podman.sock" {
		t.Fatalf("CONTAINER_HOST = %q, %v", got, err)
	}

	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	got, err = ResolveContainerHost("docker", "")
	if err != nil || got != "tcp://127.0.0.1:2375" {
		t.Fatalf("DOCKER_HOST = %q, %v", got, err)
	}
}

func TestBuildCorporateCAScriptPerFamily(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----\n")

	deb := buildCorporateCAScript(artifactDeb, pem)
	if !strings.Contains(deb, "/usr/local/share/ca-certificates/opf-corporate-ca.crt") || !strings.Contains(deb, "update-ca-certificates") {
		t.Fatalf("script CA APT inesperado:\n%s", deb)
	}
	rpm := buildCorporateCAScript(artifactRPM, pem)
	if !strings.Contains(rpm, "/etc/pki/ca-trust/source/anchors/opf-corporate-ca.crt") || !strings.Contains(rpm, "update-ca-trust extract") {
		t.Fatalf("script CA RPM inesperado:\n%s", rpm)
	}
	apk := buildCorporateCAScript(artifactAPK, pem)
	if !strings.Contains(apk, "/etc/ssl/certs/ca-certificates.crt") {
		t.Fatalf("script CA APK inesperado:\n%s", apk)
	}
}
