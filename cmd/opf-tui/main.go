package main

import (
	"fmt"
	"os"

	"opf/ui/tui"
)

func main() {
	if err := tui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "opf-tui:", err)
		os.Exit(1)
	}
}
