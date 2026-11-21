//go:build windows

// Command opf-installer is the Windows installer distributed inside
// opf-installer-windows-<arch>.zip together with a payload/ folder.
//
// It is a small raw Go executable (not packed, no embedded MSI): Cortex XDR
// blocks packed setup executables but runs this installer, which copies the
// raw .exe files, adds the install folder to the per-user PATH and creates a
// Start Menu shortcut.
//
// Profiles:
//
//  1. bundle  GUI + TUI + CLI
//  2. gui     GUI only
//  3. tui     TUI only
//  4. cli     CLI only
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type profile struct {
	files []string
}

var profiles = map[string]profile{
	"bundle": {files: []string{"opf-gui.exe", "opf-tui.exe", "opf-cli.exe"}},
	"gui":    {files: []string{"opf-gui.exe"}},
	"tui":    {files: []string{"opf-tui.exe"}},
	"cli":    {files: []string{"opf-cli.exe"}},
}

func usage() {
	fmt.Fprintln(os.Stderr, `OPF installer (Windows)

Usage:
  opf-installer.exe [--profile bundle|gui|tui|cli] [--silent] [--dir PATH]

Without --profile, an interactive menu is shown.
Profiles:
  bundle  GUI + TUI + CLI
  gui     GUI only
  tui     TUI only
  cli     CLI only

The executable must stay next to its payload/ folder (as shipped in the zip).
Default destination: %LocalAppData%\OPF`)
}

func chooseProfile() string {
	fmt.Println("Offline Package Fetcher installer")
	fmt.Println()
	fmt.Println("  1) Bundle: GUI + TUI + CLI")
	fmt.Println("  2) GUI only")
	fmt.Println("  3) TUI only")
	fmt.Println("  4) CLI only")
	fmt.Println()
	fmt.Print("Choose the installation profile [1-4]: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(line) {
	case "1":
		return "bundle"
	case "2":
		return "gui"
	case "3":
		return "tui"
	case "4":
		return "cli"
	default:
		return ""
	}
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

func createShortcut(guiPath string) error {
	shortcut := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\OPF.lnk`)
	script := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s'); $s.TargetPath='%s'; $s.WorkingDirectory='%s'; $s.Save()`,
		strings.ReplaceAll(shortcut, `'`, `''`),
		strings.ReplaceAll(guiPath, `'`, `''`),
		strings.ReplaceAll(filepath.Dir(guiPath), `'`, `''`),
	)
	// #nosec G702 -- fixed powershell.exe, single-quote-escaped paths; no shell interpolation\n	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func main() {
	profileName := ""
	installDir := ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			usage()
			return
		case "--silent":
			// Accepted for compatibility; this installer has no prompts.
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
			usage()
			os.Exit(2)
		}
	}
	if profileName == "" {
		profileName = chooseProfile()
	}
	selected, ok := profiles[profileName]
	if !ok {
		fmt.Fprintln(os.Stderr, "error: profile must be bundle, gui, tui or cli")
		os.Exit(2)
	}
	if installDir == "" {
		installDir = filepath.Join(os.Getenv("LOCALAPPDATA"), "OPF")
	}
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: locate installer: %v\n", err)
		os.Exit(1)
	}
	payloadDir := filepath.Join(filepath.Dir(exePath), "payload")
	if err := os.MkdirAll(installDir, 0o755); err != nil { // #nosec G703 -- explicit user-selected install directory
		fmt.Fprintf(os.Stderr, "error: create install folder: %v\n", err)
		os.Exit(1)
	}
	for _, name := range selected.files {
		src := filepath.Join(payloadDir, name)
		dst := filepath.Join(installDir, name)
		if err := copyFile(src, dst); err != nil {
			fmt.Fprintf(os.Stderr, "error: install %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("installed %s\n", dst)
	}
	if err := addToUserPath(installDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update PATH: %v\n", err)
	} else {
		fmt.Println("added the install folder to the user PATH")
	}
	if profileName == "bundle" || profileName == "gui" {
		if err := createShortcut(filepath.Join(installDir, "opf-gui.exe")); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create Start Menu shortcut: %v\n", err)
		}
	}
	fmt.Printf("\nOPF installed (%s) in %s\n", profileName, installDir)
	if profileName == "bundle" || profileName == "gui" {
		fmt.Printf("Launch the GUI: %s\n", filepath.Join(installDir, "opf-gui.exe"))
	}
}
