//go:build windows

// Command opf-uninstaller removes a per-user or Program Files OPF installation.
//
// The installer copies it next to the binaries and writes install.json. The
// uninstaller copies itself to %TEMP% first, so it can remove the installed
// uninstaller and the installation folder while it is not running from there.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type manifest struct {
	Version      string   `json:"version"`
	Profile      string   `json:"profile"`
	InstallDir   string   `json:"install_dir"`
	DatabasePath string   `json:"database_path"`
	Shortcut     bool     `json:"shortcut"`
	PathAdded    bool     `json:"path_added"`
	Files        []string `json:"files"`
}

var stdin = bufio.NewReader(os.Stdin)

func box(lines ...string) {
	fmt.Println("+" + strings.Repeat("-", 62) + "+")
	for _, line := range lines {
		if len(line) > 61 {
			line = line[:61]
		}
		fmt.Printf("| %-60s |\n", line)
	}
	fmt.Println("+" + strings.Repeat("-", 62) + "+")
}

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func relaunchElevated() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if arg != "--elevated" {
			args = append(args, arg)
		}
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
	}
	script := fmt.Sprintf(
		"$p = Start-Process -Verb RunAs -FilePath '%s' -ArgumentList @(%s) -PassThru; $p.WaitForExit(); exit $p.ExitCode",
		strings.ReplaceAll(exePath, "'", "''"),
		strings.Join(quoted, ","),
	)
	// #nosec G702 -- fixed powershell.exe, single-quote-escaped paths
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copySelfToTemp() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("opf-uninstaller-%d.exe", os.Getpid()))
	in, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return tmp, nil
}

func removeUserPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	current, _, err := key.GetStringValue("Path")
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	parts := strings.Split(current, ";")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.EqualFold(strings.TrimSpace(part), dir) {
			continue
		}
		kept = append(kept, part)
	}
	return key.SetExpandStringValue("Path", strings.Join(kept, ";"))
}

func removeShortcut() {
	shortcut := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\OPF.lnk`)
	_ = os.Remove(shortcut) // #nosec G703 -- fixed Start Menu path
}

func removeARP() {
	_ = registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\OPF`)
}

func confirm(label string, defaultValue bool) bool {
	def := "Y/n"
	if !defaultValue {
		def = "y/N"
	}
	for {
		fmt.Printf("%s [%s]: ", label, def)
		line, err := stdin.ReadString('\n')
		if err != nil {
			return defaultValue
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return defaultValue
		case "y", "yes", "s", "sim":
			return true
		case "n", "no", "nao", "não":
			return false
		default:
			fmt.Println("  Responda Y (sim) ou N (não).")
		}
	}
}

func loadManifest(exeDir string) (manifest, error) {
	path := filepath.Join(exeDir, "install.json")
	data, err := os.ReadFile(path) // #nosec G703 -- install directory selected by the user
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	if m.InstallDir == "" {
		m.InstallDir = exeDir
	}
	return m, nil
}

func fallbackManifest(exeDir string) manifest {
	return manifest{
		InstallDir: exeDir,
		Files:      []string{"opf-gui.exe", "opf-tui.exe", "opf-cli.exe", "uninstall-opf.exe"},
	}
}

func run() int {
	silent := false
	removeData := false
	fromTemp := false
	elevated := false
	installDirArg := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--silent":
			silent = true
		case "--remove-data":
			removeData = true
		case "--from-temp":
			fromTemp = true
		case "--install-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --install-dir requires a value")
				return 2
			}
			installDirArg = args[i+1]
			i++
		case "--elevated":
			elevated = true
		case "--help", "-h":
			fmt.Println("OPF uninstaller")
			fmt.Println("Usage: uninstall-opf.exe [--silent] [--remove-data]")
			return 0
		}
	}
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: locate uninstaller: %v\n", err)
		return 1
	}
	exeDir := filepath.Dir(exePath)

	if !fromTemp {
		tmp, err := copySelfToTemp()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: prepare uninstaller: %v\n", err)
			return 1
		}
		args := []string{"--from-temp", "--install-dir", exeDir}
		if silent {
			args = append(args, "--silent")
		}
		if removeData {
			args = append(args, "--remove-data")
		}
		cmd := exec.Command(tmp, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "error: start uninstaller: %v\n", err)
			_ = os.Remove(tmp)
			return 1
		}
		// Do not wait: the installed uninstaller is locked by this process
		// until it exits. The temporary child retries the removal and can
		// delete the installed copy once this process is gone.
		return 0
	}

	manifestDir := exeDir
	if fromTemp && installDirArg != "" {
		manifestDir = installDirArg
	}
	m, err := loadManifest(manifestDir)
	if err != nil {
		m = fallbackManifest(manifestDir)
	}
	if m.InstallDir == "" {
		m.InstallDir = manifestDir
	}
	if !elevated && !isAdmin() && isProgramFiles(m.InstallDir) {
		if err := relaunchElevated(); err != nil {
			fmt.Fprintf(os.Stderr, "error: administrator rights are required: %v\n", err)
			return 1
		}
		return 0
	}

	if !silent {
		fmt.Println()
		box("Offline Package Fetcher (OPF) - Uninstaller", "",
			"This will remove OPF from:", m.InstallDir, "")
		if !confirm("Remove OPF from this computer?", true) {
			fmt.Println("Uninstall cancelled.")
			return 0
		}
		if !removeData {
			removeData = confirm("Also remove the local database and history?", false)
		}
	}

	fmt.Println()
	fmt.Println("Removing OPF...")
	removed, failed := 0, 0
	for _, name := range m.Files {
		if name == "" || strings.ContainsAny(name, `\/`) {
			continue
		}
		path := filepath.Join(m.InstallDir, name)
		var err error
		for attempt := 0; attempt < 20; attempt++ {
			err = os.Remove(path) // #nosec G703 -- fixed file name under the install directory
			if err == nil || os.IsNotExist(err) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(os.Stderr, "  could not remove %s: %v\n", path, err)
			failed++
			continue
		}
		removed++
		fmt.Printf("  removed %s\n", name)
	}
	if m.Shortcut {
		removeShortcut()
	}
	_ = removeUserPath(m.InstallDir)
	removeARP()
	if removeData && m.DatabasePath != "" {
		if err := os.Remove(m.DatabasePath); err == nil { // #nosec G703 -- database path recorded by the installer
			fmt.Printf("  removed database %s\n", m.DatabasePath)
		}
		_ = os.Remove(filepath.Dir(m.DatabasePath))               // #nosec G703 -- parent of the recorded database
		_ = os.Remove(filepath.Dir(filepath.Dir(m.DatabasePath))) // #nosec G703 -- OPF data directory
	}
	_ = os.Remove(filepath.Join(m.InstallDir, "install.json")) // #nosec G703 -- install directory selected by the user
	_ = os.Remove(m.InstallDir)                                // #nosec G703 -- installation directory selected by the user
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\nUninstall finished with %d file(s) that could not be removed.\n", failed)
		return 1
	}
	fmt.Println()
	box("OPF was removed successfully.", "", fmt.Sprintf("%d file(s) removed.", removed))
	return 0
}

func isProgramFiles(dir string) bool {
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		rel, err := filepath.Rel(base, dir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func main() {
	os.Exit(run())
}
