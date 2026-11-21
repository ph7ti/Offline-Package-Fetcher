package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opf/internal/core"
	"opf/internal/transfer"
)

type TargetInfo struct {
	OS         string
	Version    string
	Arch       string
	Family     string
	Image      string
	DistroID   string
	PrettyName string
	Tested     bool
}

// DetectRemoteTarget opens a transient SSH connection, inspects /etc/os-release
// and uname, and returns the normalized fetch target.
func DetectRemoteTarget(ctx context.Context, cfg core.SSHConfig) (*TargetInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	manager := transfer.NewSSHTransferManager()
	if err := manager.Connect(probeCtx, cfg); err != nil {
		return nil, err
	}
	defer manager.Close()
	return DetectTargetOn(probeCtx, manager)
}

func DetectTargetOn(ctx context.Context, tm core.TransferManager) (*TargetInfo, error) {
	if tm == nil {
		return nil, fmt.Errorf("transferência SSH não configurada")
	}
	osRelease, err := tm.RunCommand(ctx, "cat /etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("ler /etc/os-release: %w", err)
	}
	archOut, err := tm.RunCommand(ctx, "uname -m")
	if err != nil {
		return nil, fmt.Errorf("ler arquitetura: %w", err)
	}
	return ParseTarget(osRelease, strings.TrimSpace(archOut))
}

func ParseTarget(osRelease, unameArch string) (*TargetInfo, error) {
	values := parseOSRelease(osRelease)
	id := strings.ToLower(strings.TrimSpace(values["ID"]))
	version := strings.TrimSpace(values["VERSION_ID"])
	like := strings.ToLower(values["ID_LIKE"])
	if version == "" {
		version = strings.TrimSpace(values["VERSION"])
	}
	if version == "" {
		return nil, fmt.Errorf("VERSION_ID ausente em /etc/os-release")
	}

	osName := normalizeDistroID(id, like)
	if osName == "" {
		return nil, fmt.Errorf("distro não reconhecida: ID=%q ID_LIKE=%q", id, like)
	}
	family, err := familyForOS(osName)
	if err != nil {
		return nil, err
	}
	arch, err := normalizedTargetArch(unameArch, family)
	if err != nil {
		return nil, err
	}
	image, tested := presetImage(osName, version)
	return &TargetInfo{
		OS: osName, Version: version, Arch: arch, Family: family,
		Image: image, DistroID: id, PrettyName: values["PRETTY_NAME"], Tested: tested,
	}, nil
}

func normalizeDistroID(id, like string) string {
	switch id {
	case "ubuntu", "debian", "alpine", "centos", "rhel", "fedora", "almalinux", "rockylinux":
		return id
	case "rocky":
		return "rockylinux"
	case "ol":
		return "rhel"
	}
	for _, token := range strings.Fields(like) {
		switch token {
		case "ubuntu":
			return "ubuntu"
		case "debian":
			return "debian"
		case "alpine":
			return "alpine"
		case "centos":
			return "centos"
		case "rhel", "fedora":
			return "rhel"
		}
	}
	return ""
}

func normalizedTargetArch(arch, family string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		if family == "apt" {
			return "amd64", nil
		}
		return "x86_64", nil
	case "aarch64", "arm64":
		if family == "apt" {
			return "arm64", nil
		}
		return "aarch64", nil
	case "armv7l", "armv7", "armhf":
		switch family {
		case "apt":
			return "armhf", nil
		case "rpm":
			return "armv7hl", nil
		default:
			return "armv7", nil
		}
	case "i686", "i386":
		if family == "apt" {
			return "i386", nil
		}
		return "i686", nil
	default:
		return "", fmt.Errorf("arquitetura não suportada: %q", arch)
	}
}

func presetImage(osName, version string) (string, bool) {
	tested := false
	image := ""
	switch osName {
	case "ubuntu":
		image = "ubuntu:" + version
		tested = version == "22.04" || version == "24.04"
	case "debian":
		image = "debian:" + version
		tested = version == "12"
	case "alpine":
		image = "alpine:" + version
		tested = version == "3.20"
	case "centos":
		if version == "7" {
			image = "centos:7"
			tested = true
		} else {
			image = "quay.io/centos/centos:stream" + strings.SplitN(version, ".", 2)[0]
			tested = version == "9" || version == "10"
		}
	case "rockylinux":
		image = "rockylinux:" + version
		tested = strings.HasPrefix(version, "9")
	case "almalinux":
		image = "almalinux:" + version
		tested = strings.HasPrefix(version, "9")
	case "fedora":
		image = "fedora:" + version
		tested = version == "41"
	case "rhel":
		major := strings.SplitN(version, ".", 2)[0]
		image = "registry.access.redhat.com/ubi" + major + "/ubi"
		tested = major == "8" || major == "9"
	}
	return image, tested
}

func parseOSRelease(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(value, `"'`)
	}
	return values
}
