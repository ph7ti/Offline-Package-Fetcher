//go:build !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "opf-gui requer CGO_ENABLED=1 e as bibliotecas gráficas do Fyne; use make build-gui")
	os.Exit(1)
}
