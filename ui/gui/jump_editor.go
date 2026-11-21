//go:build cgo

package gui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	opfapp "opf/internal/app"
	"opf/internal/core"
)

var jumpAuthLabels = []string{"Senha", "Chave", "Chave + senha"}

type jumpRow struct {
	host       *widget.Entry
	port       *widget.Entry
	user       *widget.Entry
	auth       *widget.Select
	password   *widget.Entry
	keyPath    *widget.Entry
	knownHosts *widget.Entry
}

func newJumpRow() *jumpRow {
	row := &jumpRow{
		host:       widget.NewEntry(),
		port:       widget.NewEntry(),
		user:       widget.NewEntry(),
		auth:       widget.NewSelect(jumpAuthLabels, nil),
		password:   widget.NewPasswordEntry(),
		keyPath:    widget.NewEntry(),
		knownHosts: widget.NewEntry(),
	}
	row.host.SetPlaceHolder("host ou IP da ponte")
	row.port.SetText("22")
	row.user.SetPlaceHolder("usuário da ponte")
	row.password.SetPlaceHolder("senha da ponte (somente em memória)")
	row.keyPath.SetPlaceHolder("~/.ssh/id_ed25519")
	row.knownHosts.SetPlaceHolder("vazio herda o known_hosts do destino")
	row.auth.OnChanged = func(string) { row.syncAuth() }
	row.auth.SetSelected(jumpAuthLabels[0])
	row.syncAuth()
	return row
}

func (r *jumpRow) authMode() string {
	mode, err := jumpAuthMode(r.auth.Selected)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(r.auth.Selected))
	}
	return mode
}

func (r *jumpRow) syncAuth() {
	switch r.authMode() {
	case opfapp.SSHJumpAuthKey:
		r.password.Disable()
		r.keyPath.Enable()
	case opfapp.SSHJumpAuthKeyPassword:
		r.password.Enable()
		r.keyPath.Enable()
	default:
		r.password.Enable()
		r.keyPath.Disable()
	}
}

func (r *jumpRow) input() opfapp.SSHJumpInput {
	return opfapp.SSHJumpInput{
		Host:       r.host.Text,
		Port:       r.port.Text,
		User:       r.user.Text,
		AuthMode:   r.authMode(),
		Password:   r.password.Text,
		KeyPath:    r.keyPath.Text,
		KnownHosts: r.knownHosts.Text,
	}
}

func (r *jumpRow) config(base core.SSHConfig) (core.SSHConfig, error) {
	jumps, err := opfapp.BuildSSHJumpHosts(base, []opfapp.SSHJumpInput{r.input()})
	if err != nil {
		return core.SSHConfig{}, err
	}
	return jumps[0], nil
}

func (r *jumpRow) metadata() opfapp.SSHJumpMetadata {
	return opfapp.SSHJumpMetadataFromInputs([]opfapp.SSHJumpInput{r.input()})[0]
}

func (r *jumpRow) setMetadata(meta opfapp.SSHJumpMetadata) {
	r.host.SetText(meta.Host)
	port := meta.Port
	if port == 0 {
		port = 22
	}
	r.port.SetText(fmt.Sprintf("%d", port))
	r.user.SetText(meta.User)
	r.auth.SetSelected(jumpAuthLabel(meta.AuthMode))
	r.keyPath.SetText(meta.KeyPath)
	r.knownHosts.SetText(meta.KnownHosts)
	r.password.SetText("")
	r.syncAuth()
}

func jumpAuthLabel(mode string) string {
	switch mode {
	case opfapp.SSHJumpAuthKey:
		return "Chave"
	case opfapp.SSHJumpAuthKeyPassword:
		return "Chave + senha"
	case "", opfapp.SSHJumpAuthPassword:
		return "Senha"
	default:
		return "Senha"
	}
}

func jumpAuthMode(label string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "senha", "password":
		return opfapp.SSHJumpAuthPassword, nil
	case "chave", "key":
		return opfapp.SSHJumpAuthKey, nil
	case "chave + senha", "chave+senha", "chave e senha", "key+password":
		return opfapp.SSHJumpAuthKeyPassword, nil
	default:
		return "", fmt.Errorf("modo de autenticação inválido: %q", label)
	}
}

func (u *UI) jumpInputs() []opfapp.SSHJumpInput {
	inputs := make([]opfapp.SSHJumpInput, 0, len(u.jumpRows))
	for _, row := range u.jumpRows {
		inputs = append(inputs, row.input())
	}
	return inputs
}

func (u *UI) jumpFingerprint() string {
	return opfapp.SSHJumpFingerprint(u.jumpInputs())
}

func (u *UI) buildJumpConfigs(base core.SSHConfig) ([]core.SSHConfig, error) {
	return opfapp.BuildSSHJumpHosts(base, u.jumpInputs())
}

func (u *UI) addJumpRow() {
	u.jumpRows = append(u.jumpRows, newJumpRow())
	u.refreshJumpList()
}

func (u *UI) removeJumpRow(index int) {
	if index < 0 || index >= len(u.jumpRows) {
		return
	}
	u.jumpRows = append(u.jumpRows[:index], u.jumpRows[index+1:]...)
	u.refreshJumpList()
}

func (u *UI) moveJumpRow(index, delta int) {
	target := index + delta
	if index < 0 || index >= len(u.jumpRows) || target < 0 || target >= len(u.jumpRows) {
		return
	}
	u.jumpRows[index], u.jumpRows[target] = u.jumpRows[target], u.jumpRows[index]
	u.refreshJumpList()
}

func (u *UI) clearJumpRows() {
	u.jumpRows = nil
	u.refreshJumpList()
}

// setJumpMetadata repopulates the bridge rows from persisted metadata,
// leaving every password blank. It reports whether any bridge will require a
// password before execution.
func (u *UI) setJumpMetadata(metas []opfapp.SSHJumpMetadata) bool {
	u.jumpRows = nil
	needsPassword := false
	for _, meta := range metas {
		row := newJumpRow()
		row.setMetadata(meta)
		u.jumpRows = append(u.jumpRows, row)
		if meta.AuthMode != opfapp.SSHJumpAuthKey {
			needsPassword = true
		}
	}
	return needsPassword
}

func (u *UI) refreshJumpList() {
	if u.jumpList == nil {
		return
	}
	objects := make([]fyne.CanvasObject, 0, len(u.jumpRows)+1)
	if len(u.jumpRows) == 0 {
		objects = append(objects, u.banner("Nenhuma ponte SSH. A conexão vai direto ao destino.", toneInfo))
	} else {
		for i, row := range u.jumpRows {
			if i > 0 {
				objects = append(objects, vSpace(10))
			}
			objects = append(objects, u.jumpRowCard(row, i, len(u.jumpRows)))
		}
	}
	u.jumpList.Objects = objects
	u.jumpList.Refresh()
}

func (u *UI) jumpRowCard(row *jumpRow, index, total int) fyne.CanvasObject {
	up := u.ghostButton("Subir", theme.MoveUpIcon(), func() { u.moveJumpRow(index, -1) })
	down := u.ghostButton("Descer", theme.MoveDownIcon(), func() { u.moveJumpRow(index, 1) })
	remove := u.ghostButton("Remover", theme.DeleteIcon(), func() { u.removeJumpRow(index) })
	if index == 0 {
		up.Disable()
	}
	if index == total-1 {
		down.Disable()
	}
	header := container.NewHBox(
		textLine(fmt.Sprintf("Ponte %d", index+1), 13, true, theme.Color(theme.ColorNameForeground)),
		layout.NewSpacer(),
		up,
		down,
		remove,
	)
	form := widget.NewForm(
		widget.NewFormItem("Host", row.host),
		widget.NewFormItem("Porta", row.port),
		widget.NewFormItem("Usuário", row.user),
		widget.NewFormItem("Autenticação", row.auth),
		widget.NewFormItem("Senha", row.password),
		widget.NewFormItem("Chave privada", row.keyPath),
		widget.NewFormItem("Known hosts", row.knownHosts),
	)
	note := u.muted("Credenciais da ponte não são herdadas do destino nem persistidas.", theme.SizeNameCaptionText)
	return u.cardShell(theme.Color(colorCardBackground), theme.Color(colorBorder), 1, container.NewVBox(
		header,
		vSpace(6),
		form,
		vSpace(6),
		note,
	))
}
