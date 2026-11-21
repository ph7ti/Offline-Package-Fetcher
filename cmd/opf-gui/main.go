//go:build cgo

package main

import (
	"fmt"
	"os"

	"opf/ui/gui"
)

func main() {
	if err := gui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "opf-gui:", err)
		os.Exit(1)
	}
}
