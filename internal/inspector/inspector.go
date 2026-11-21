package inspector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"opf/internal/core"
)

var packageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._:-]*$`)

// Inspector performs the remote pre-check using the target package manager.
type Inspector struct {
	family string
	tm     core.TransferManager
}

var _ core.PackageInspector = (*Inspector)(nil)

func New(family string, tm core.TransferManager) (*Inspector, error) {
	switch family {
	case "apt", "rpm", "apk":
	default:
		return nil, fmt.Errorf("família de pré-check não suportada: %q", family)
	}
	if tm == nil {
		return nil, errors.New("transferência SSH não configurada para o pré-check")
	}
	return &Inspector{family: family, tm: tm}, nil
}

func (i *Inspector) InspectPackages(ctx context.Context, pkgs []string) ([]core.InstalledPackage, error) {
	names := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" {
			continue
		}
		if !packageNamePattern.MatchString(pkg) {
			return nil, fmt.Errorf("nome de pacote inválido no pré-check: %q", pkg)
		}
		names = append(names, shellQuote(pkg))
	}
	if len(names) == 0 {
		return nil, errors.New("nenhum pacote para pré-check")
	}

	var cmd string
	switch i.family {
	case "apt":
		cmd = "dpkg-query -W -f='${db:Status-Abbrev}\\t${Package}\\t${Version}\\t${Architecture}\\n' " +
			strings.Join(names, " ") + " 2>/dev/null || true"
	case "rpm":
		var builder strings.Builder
		for _, name := range names {
			builder.WriteString("rpm -q --qf '%{NAME}\\t%{VERSION}-%{RELEASE}\\t%{ARCH}\\n' ")
			builder.WriteString(name)
			builder.WriteString(" 2>/dev/null || true; ")
		}
		cmd = builder.String()
	case "apk":
		var builder strings.Builder
		for _, name := range names {
			builder.WriteString("if apk info -e ")
			builder.WriteString(name)
			builder.WriteString(" >/dev/null 2>&1; then apk info -v ")
			builder.WriteString(name)
			builder.WriteString(" 2>/dev/null; fi; ")
		}
		cmd = builder.String()
	default:
		return nil, fmt.Errorf("família de pré-check não suportada: %q", i.family)
	}

	output, err := i.tm.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("executar pré-check %s: %w", i.family, err)
	}
	return parseInstalled(i.family, output)
}

func parseInstalled(family, output string) ([]core.InstalledPackage, error) {
	var installed []core.InstalledPackage
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch family {
		case "apt":
			fields := strings.Split(line, "\t")
			if len(fields) != 4 {
				continue
			}
			status := strings.TrimSpace(fields[0])
			if !strings.HasPrefix(status, "ii") {
				continue
			}
			installed = append(installed, core.InstalledPackage{
				Name: fields[1], Version: fields[2], Arch: fields[3],
			})
		case "rpm":
			fields := strings.Split(line, "\t")
			if len(fields) != 3 {
				continue
			}
			installed = append(installed, core.InstalledPackage{
				Name: fields[0], Version: fields[1], Arch: fields[2],
			})
		case "apk":
			name, version, err := splitAPKVersion(line)
			if err != nil {
				continue
			}
			installed = append(installed, core.InstalledPackage{Name: name, Version: version})
		}
	}
	return installed, nil
}

func splitAPKVersion(value string) (string, string, error) {
	revision := strings.LastIndex(value, "-r")
	if revision <= 0 {
		return "", "", fmt.Errorf("versão APK inválida: %q", value)
	}
	versionStart := strings.LastIndex(value[:revision], "-")
	if versionStart <= 0 {
		return "", "", fmt.Errorf("versão APK inválida: %q", value)
	}
	return value[:versionStart], value[versionStart+1:], nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
