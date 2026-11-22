//go:build windows

// Command opf-installer is the Windows installer distributed inside
// opf-installer-windows-<arch>.zip together with a payload/ folder.
//
// It is a raw Go executable (no packed setup, no MSI), which Cortex XDR
// accepts. It implements a small console wizard:
//
//  1. installation profile (bundle / GUI / TUI / CLI)
//  2. installation folder (default: %ProgramFiles%\OPF)
//  3. optional Start Menu shortcut
//  4. per-user SQLite database at %LocalAppData%\OPF\data\opf.db
//  5. PATH update, progress display, and optional launch of GUI/TUI
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

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var version = "dev"

type profile struct {
	title string
	files []string
}

var profiles = map[string]profile{
	"bundle": {title: "Bundle: GUI + TUI + CLI", files: []string{"opf-gui.exe", "opf-tui.exe", "opf-cli.exe"}},
	"gui":    {title: "GUI only", files: []string{"opf-gui.exe"}},
	"tui":    {title: "TUI only", files: []string{"opf-tui.exe"}},
	"cli":    {title: "CLI only", files: []string{"opf-cli.exe"}},
}

var stdin = bufio.NewReader(os.Stdin)

type installManifest struct {
	Version      string   `json:"version"`
	Profile      string   `json:"profile"`
	InstallDir   string   `json:"install_dir"`
	DatabasePath string   `json:"database_path"`
	Shortcut     bool     `json:"shortcut"`
	PathAdded    bool     `json:"path_added"`
	Files        []string `json:"files"`
}

func box(lines ...string) {
	width := 64
	fmt.Println("+" + strings.Repeat("-", width) + "+")
	for _, line := range lines {
		if len(line) > width {
			line = line[:width]
		}
		fmt.Printf("| %-*s |\n", width-1, line)
	}
	fmt.Println("+" + strings.Repeat("-", width) + "+")
}

func ask(label, defaultValue string) string {
	fmt.Printf("%s [%s]: ", label, defaultValue)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return defaultValue
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue
	}
	return line
}

func askYesNo(label string, defaultValue bool) bool {
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
			fmt.Println("  Please answer Y (yes) or N (no).")
		}
	}
}

func chooseProfile() string {
	fmt.Println()
	fmt.Println("Step 1 of 4 - Choose what to install:")
	fmt.Println("  1) Bundle: GUI + TUI + CLI")
	fmt.Println("  2) GUI only")
	fmt.Println("  3) TUI only")
	fmt.Println("  4) CLI only")
	for {
		choice := ask("Profile", "1")
		switch choice {
		case "1":
			return "bundle"
		case "2":
			return "gui"
		case "3":
			return "tui"
		case "4":
			return "cli"
		default:
			fmt.Println("  Please type 1, 2, 3 or 4.")
		}
	}
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
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G703 -- explicit user-selected installation folder
		return err
	}
	probe := filepath.Join(dir, ".opf-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G703 -- write probe in the user-selected installation folder
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(probe) // #nosec G703 -- remove the probe created above
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) // #nosec G703 -- fixed payload name under the user-selected install directory
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func addToUserPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	current, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, part := range strings.Split(current, ";") {
		if strings.EqualFold(strings.TrimSpace(part), dir) {
			return nil
		}
	}
	updated := dir
	if strings.TrimSpace(current) != "" {
		updated = strings.TrimRight(current, ";") + ";" + dir
	}
	return key.SetExpandStringValue("Path", updated)
}

func writeManifest(path string, m installManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644) // #nosec G703 -- install manifest written to the user-selected folder
}

func registerUninstall(m installManifest) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\OPF`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	uninstaller := filepath.Join(m.InstallDir, "uninstall-opf.exe")
	icon := filepath.Join(m.InstallDir, "opf-gui.exe")
	if _, err := os.Stat(icon); err != nil {
		icon = filepath.Join(m.InstallDir, "opf-cli.exe")
	}
	values := map[string]string{
		"DisplayName":          "Offline Package Fetcher",
		"DisplayVersion":       version,
		"Publisher":            "Offline Package Fetcher",
		"InstallLocation":      m.InstallDir,
		"DisplayIcon":          icon,
		"UninstallString":      `"` + uninstaller + `"`,
		"QuietUninstallString": `"` + uninstaller + `" --silent`,
		"URLInfoAbout":         "https://github.com/ph7ti/Offline-Package-Fetcher",
	}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return err
		}
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if err := key.SetDWordValue(name, 1); err != nil {
			return err
		}
	}
	return nil
}

func createShortcut(guiPath string) error {
	shortcut := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\OPF.lnk`)
	script := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s'); $s.TargetPath='%s'; $s.WorkingDirectory='%s'; $s.Save()`,
		strings.ReplaceAll(shortcut, `'`, `''`),
		strings.ReplaceAll(guiPath, `'`, `''`),
		strings.ReplaceAll(filepath.Dir(guiPath), `'`, `''`),
	)
	// #nosec G702 -- fixed powershell.exe, single-quote-escaped paths; no shell interpolation
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func localDataDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return dir
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return os.TempDir()
	}
	return dir
}

func createDatabase() (string, error) {
	dir := filepath.Join(localDataDir(), "OPF", "data")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G703 -- explicit user-selected installation folder // #nosec G703 -- per-user fixed path
		return "", err
	}
	db := filepath.Join(dir, "opf.db")
	f, err := os.OpenFile(db, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return db, nil
}

func launch(profileName, installDir string) {
	gui := filepath.Join(installDir, "opf-gui.exe")
	tui := filepath.Join(installDir, "opf-tui.exe")
	fmt.Println()
	fmt.Println("Step 4 of 4 - Open OPF:")
	options := []string{}
	if profileName == "bundle" || profileName == "gui" {
		options = append(options, "Open the GUI")
	}
	if profileName == "bundle" || profileName == "tui" {
		options = append(options, "Open the TUI in a new window")
	}
	options = append(options, "Finish without opening")
	for i, option := range options {
		fmt.Printf("  %d) %s\n", i+1, option)
	}
	choice := ask("Option", fmt.Sprintf("%d", len(options)))
	index := 0
	if n, err := fmt.Sscanf(choice, "%d", &index); err != nil || n != 1 || index < 1 || index > len(options) {
		index = len(options)
	}
	selected := options[index-1]
	switch {
	case selected == "Open the GUI":
		if err := exec.Command(gui).Start(); err != nil { // #nosec G702 -- fixed installed GUI path
			fmt.Printf("  Could not open the GUI: %v\n", err)
		}
	case strings.HasPrefix(selected, "Open the TUI"):
		cmd := exec.Command("cmd.exe", "/c", "start", "OPF TUI", tui) // #nosec G702 -- fixed cmd.exe and installed TUI path
		cmd.Dir = installDir
		if err := cmd.Start(); err != nil {
			fmt.Printf("  Could not open the TUI: %v\n", err)
		}
	}
}

func main() {
	profileName := ""
	installDir := ""
	silent := false
	noShortcut := false
	noLaunch := false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Printf("OPF installer %s (Windows)\n\n", version)
			fmt.Println("Usage: opf-installer.exe [--profile bundle|gui|tui|cli] [--dir PATH]")
			fmt.Println("       [--silent] [--no-shortcut] [--no-launch] [--elevated]")
			return
		case "--silent":
			silent = true
		case "--no-shortcut":
			noShortcut = true
		case "--no-launch":
			noLaunch = true
		case "--elevated":
			// Internal flag used after UAC elevation.
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --profile requires a value")
				os.Exit(2)
			}
			profileName = args[i+1]
			i++
		case "--dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --dir requires a value")
				os.Exit(2)
			}
			installDir = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "error: unknown argument %q\n", args[i])
			os.Exit(2)
		}
	}

	if !silent {
		box(
			"Offline Package Fetcher (OPF) - Installer",
			"Version "+version,
			"",
			"This wizard installs OPF on your computer.",
			"You can change the folder and choose what to install.",
		)
	}
	if profileName == "" {
		if silent {
			profileName = "bundle"
		} else {
			profileName = chooseProfile()
		}
	}
	selected, ok := profiles[profileName]
	if !ok {
		fmt.Fprintln(os.Stderr, "error: profile must be bundle, gui, tui or cli")
		os.Exit(2)
	}
	if installDir == "" {
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		installDir = filepath.Join(programFiles, "OPF")
	}
	if !silent {
		fmt.Println()
		fmt.Println("Step 2 of 4 - Installation folder:")
		fmt.Printf("  Default: %s\n", installDir)
		installDir = ask("Install folder", installDir)
	}
	installDir = filepath.Clean(installDir)

	if err := writable(installDir); err != nil {
		if isAdmin() {
			fmt.Fprintf(os.Stderr, "\nCannot write to %s: %v\n", installDir, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\nCannot write to %s: %v\n", installDir, err)
		if silent {
			fmt.Fprintln(os.Stderr, "Run the installer as administrator or choose a folder under your user profile.")
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("OPF can install with administrator rights, or you can choose")
		fmt.Println("a folder inside your user profile (no administrator rights).")
		fmt.Println("  1) Restart the installer as administrator")
		fmt.Println("  2) Choose another folder")
		fmt.Println("  3) Cancel")
		switch ask("Option", "2") {
		case "1":
			if err := relaunchElevated(); err != nil {
				fmt.Fprintf(os.Stderr, "Could not restart as administrator: %v\n", err)
				os.Exit(1)
			}
			return
		case "2":
			installDir = ask("Install folder", filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OPF"))
			if err := writable(installDir); err != nil {
				fmt.Fprintf(os.Stderr, "Cannot write to %s: %v\n", installDir, err)
				os.Exit(1)
			}
		default:
			fmt.Println("Installation cancelled.")
			return
		}
	}

	shortcut := false
	if profileName == "bundle" || profileName == "gui" {
		if silent {
			shortcut = !noShortcut
		} else {
			fmt.Println()
			fmt.Println("Step 3 of 4 - Shortcuts:")
			shortcut = askYesNo("Create a Start Menu shortcut for OPF?", true)
		}
	}

	fmt.Println()
	fmt.Println("Installing OPF...")
	fmt.Printf("  [1/4] Installing %s\n", selected.title)
	payloadDir := filepath.Join(filepath.Dir(mustExecutable()), "payload")
	files := append([]string{}, selected.files...)
	files = append(files, "uninstall-opf.exe")
	for i, name := range files {
		src := filepath.Join(payloadDir, name)
		dst := filepath.Join(installDir, name)
		fmt.Printf("        (%d/%d) %s\n", i+1, len(files), name)
		if err := copyFile(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "\nInstallation failed while copying %s: %v\n", name, err)
			os.Exit(1)
		}
	}
	fmt.Println("  [2/4] Updating your user PATH")
	if err := addToUserPath(installDir); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not update PATH: %v\n", err)
	} else {
		fmt.Println("        Added to PATH.")
	}
	fmt.Println("  [3/4] Creating the local database")
	dbPath, err := createDatabase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not create %s: %v\n", dbPath, err)
	}
	m := installManifest{
		Version:      version,
		Profile:      profileName,
		InstallDir:   installDir,
		DatabasePath: dbPath,
		Shortcut:     shortcut,
		PathAdded:    true,
		Files:        files,
	}
	if err := writeManifest(filepath.Join(installDir, "install.json"), m); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not write install manifest: %v\n", err)
	}
	if err := registerUninstall(m); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not register the uninstaller: %v\n", err)
	}
	if shortcut {
		fmt.Println("  [4/4] Creating the Start Menu shortcut")
		if err := createShortcut(filepath.Join(installDir, "opf-gui.exe")); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not create shortcut: %v\n", err)
		}
	} else {
		fmt.Println("  [4/4] Shortcut skipped")
	}

	fmt.Println()
	box(
		"Installation completed successfully.",
		"",
		"Folder:   "+installDir,
		"Database: "+dbPath,
		"Profile:  "+selected.title,
	)
	if profileName == "bundle" || profileName == "gui" {
		fmt.Println("You can open the GUI from the Start Menu" + func() string {
			if shortcut {
				return "."
			}
			return " or from the installation folder."
		}())
	}
	if profileName == "cli" {
		fmt.Println("Open a new terminal and run: opf-cli -h")
	}
	if !silent && !noLaunch && profileName != "cli" {
		launch(profileName, installDir)
	}
}

func mustExecutable() string {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: locate installer: %v\n", err)
		os.Exit(1)
	}
	return exePath
}
