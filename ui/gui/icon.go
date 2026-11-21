//go:build cgo

package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/icon.png
var appIconPNG []byte

var appIcon = fyne.NewStaticResource("opf-icon.png", appIconPNG)
