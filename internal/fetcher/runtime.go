package fetcher

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveContainerHost determines the Docker-compatible daemon URI for either
// "docker" (including rootless Docker) or "podman" (including rootless Podman).
// An empty return value for "docker" lets the Docker SDK use its default socket.
func ResolveContainerHost(runtimeKind, explicitHost string) (string, error) {
	explicitHost = strings.TrimSpace(explicitHost)
	if explicitHost != "" {
		if strings.ContainsAny(explicitHost, "\r\n\x00") {
			return "", fmt.Errorf("container host inválido: %q", explicitHost)
		}
		return explicitHost, nil
	}

	kind := strings.ToLower(strings.TrimSpace(runtimeKind))
	if kind == "" {
		kind = "docker"
	}

	switch kind {
	case "docker":
		if env := strings.TrimSpace(os.Getenv("DOCKER_HOST")); env != "" {
			return env, nil
		}
		if runtime.GOOS == "linux" {
			if _, err := os.Stat("/var/run/docker.sock"); err != nil {
				if xdg, ok := validatedRuntimeDir(os.Getenv("XDG_RUNTIME_DIR")); ok {
					rootless := filepath.Join(xdg, "docker.sock")
					// #nosec G703 -- probing standard rootless Docker socket under validated XDG_RUNTIME_DIR
					if _, sockErr := os.Stat(rootless); sockErr == nil {
						return "unix://" + rootless, nil
					}
				}
			}
		}
		return "", nil

	case "podman":
		if env := strings.TrimSpace(os.Getenv("CONTAINER_HOST")); env != "" {
			return env, nil
		}
		if env := strings.TrimSpace(os.Getenv("DOCKER_HOST")); env != "" {
			return env, nil
		}
		for _, candidate := range defaultPodmanSockets() {
			if candidate.checkFile {
				// #nosec G703 -- probing standard local Podman socket path
				if _, err := os.Stat(candidate.path); err == nil {
					return candidate.uri, nil
				}
			} else {
				return candidate.uri, nil
			}
		}
		return "", fmt.Errorf("socket Podman não encontrado; inicie 'systemctl --user start podman.socket' (ou 'podman machine start') ou defina CONTAINER_HOST / --container-host")

	default:
		return "", fmt.Errorf("runtime de contêiner inválido: %q", runtimeKind)
	}
}

type socketCandidate struct {
	uri       string
	path      string
	checkFile bool
}

func validatedRuntimeDir(raw string) (string, bool) {
	dir := strings.TrimSpace(raw)
	if dir == "" || strings.ContainsAny(dir, "\r\n\x00") {
		return "", false
	}
	cleaned := filepath.Clean(dir)
	if !filepath.IsAbs(cleaned) {
		return "", false
	}
	return cleaned, true
}

func defaultPodmanSockets() []socketCandidate {
	if runtime.GOOS == "windows" {
		return []socketCandidate{
			{uri: "npipe:////./pipe/podman-machine-default", checkFile: false},
		}
	}
	var list []socketCandidate
	if xdg, ok := validatedRuntimeDir(os.Getenv("XDG_RUNTIME_DIR")); ok {
		p := filepath.Join(xdg, "podman", "podman.sock")
		list = append(list, socketCandidate{uri: "unix://" + p, path: p, checkFile: true})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		macSock := filepath.Join(home, ".local", "share", "containers", "podman", "machine", "podman.sock")
		list = append(list, socketCandidate{uri: "unix://" + macSock, path: macSock, checkFile: true})
	}
	list = append(list, socketCandidate{uri: "unix:///run/podman/podman.sock", path: "/run/podman/podman.sock", checkFile: true})
	return list
}

func buildCorporateCAScript(kind artifactKind, caPEM []byte) string {
	script := newRepositoryScript()
	switch kind {
	case artifactRPM:
		const caPath = "/etc/pki/ca-trust/source/anchors/opf-corporate-ca.crt"
		script.addFile(caPath, caPEM, "0644")
		script.addCommand("if command -v update-ca-trust >/dev/null 2>&1; then update-ca-trust extract >/dev/null 2>&1 || true; fi")
	case artifactAPK:
		const caPath = "/usr/local/share/ca-certificates/opf-corporate-ca.crt"
		script.addFile(caPath, caPEM, "0644")
		script.addCommand("mkdir -p /etc/ssl/certs && touch /etc/ssl/certs/ca-certificates.crt && cat " +
			quoteRepositoryPath(caPath) + " >> /etc/ssl/certs/ca-certificates.crt")
		script.addCommand("if command -v update-ca-certificates >/dev/null 2>&1; then update-ca-certificates >/dev/null 2>&1 || true; fi")
	default:
		const caPath = "/usr/local/share/ca-certificates/opf-corporate-ca.crt"
		script.addFile(caPath, caPEM, "0644")
		script.addCommand("if command -v update-ca-certificates >/dev/null 2>&1; then update-ca-certificates >/dev/null 2>&1 || true; " +
			"else mkdir -p /etc/ssl/certs && touch /etc/ssl/certs/ca-certificates.crt && cat " +
			quoteRepositoryPath(caPath) + " >> /etc/ssl/certs/ca-certificates.crt; fi")
	}
	return script.String()
}
