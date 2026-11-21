//go:build !cgo

package gui

import "errors"

func Run() error {
	return errors.New("GUI Fyne requer CGO_ENABLED=1")
}
