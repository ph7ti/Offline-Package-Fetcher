package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	opfapp "opf/internal/app"
	"opf/internal/core"
	"opf/internal/i18n"
)

// jumpDraft is the in-memory state of one SSH bridge. Passwords never leave
// this struct and are never persisted.
type jumpDraft struct {
	host          string
	port          string
	user          string
	authMode      string
	password      string
	keyPath       string
	keyPassphrase string
	knownHosts    string
}

func newJumpDraft() jumpDraft {
	return jumpDraft{port: "22", authMode: opfapp.SSHJumpAuthPassword}
}

func (d jumpDraft) input() opfapp.SSHJumpInput {
	return opfapp.SSHJumpInput{
		Host:          d.host,
		Port:          d.port,
		User:          d.user,
		AuthMode:      d.authMode,
		Password:      d.password,
		KeyPath:       d.keyPath,
		KeyPassphrase: d.keyPassphrase,
		KnownHosts:    d.knownHosts,
	}
}

func (d *jumpDraft) setMetadata(meta opfapp.SSHJumpMetadata) {
	d.host = meta.Host
	port := meta.Port
	if port <= 0 {
		port = 22
	}
	d.port = strconv.Itoa(port)
	d.user = meta.User
	d.authMode = meta.AuthMode
	if d.authMode == "" {
		d.authMode = opfapp.SSHJumpAuthPassword
	}
	d.keyPath = meta.KeyPath
	d.knownHosts = meta.KnownHosts
	d.password = ""
	d.keyPassphrase = ""
}

func (d jumpDraft) metadata() opfapp.SSHJumpMetadata {
	return opfapp.SSHJumpMetadataFromInputs([]opfapp.SSHJumpInput{d.input()})[0]
}

func (d jumpDraft) summary() string {
	port := 22
	if parsed, err := strconv.Atoi(strings.TrimSpace(d.port)); err == nil && parsed > 0 {
		port = parsed
	}
	host := strings.TrimSpace(d.host)
	if host == "" {
		host = "?"
	}
	user := strings.TrimSpace(d.user)
	if user == "" {
		user = "?"
	}
	return user + "@" + host + ":" + strconv.Itoa(port) + " • " + jumpAuthDisplay(d.authMode)
}

func jumpAuthDisplay(mode string) string {
	switch mode {
	case opfapp.SSHJumpAuthAgent:
		return "ssh-agent"
	case opfapp.SSHJumpAuthKey:
		return i18n.T("chave")
	case opfapp.SSHJumpAuthKeyPassword:
		return i18n.T("chave+senha")
	default:
		return i18n.T("senha")
	}
}

func (m *Model) jumpInputs() []opfapp.SSHJumpInput {
	inputs := make([]opfapp.SSHJumpInput, 0, len(m.jumpDrafts))
	for _, draft := range m.jumpDrafts {
		inputs = append(inputs, draft.input())
	}
	return inputs
}

func (m *Model) jumpFingerprint() string {
	return opfapp.SSHJumpFingerprint(m.jumpInputs())
}

func (m *Model) buildJumpHosts(base core.SSHConfig) ([]core.SSHConfig, error) {
	return opfapp.BuildSSHJumpHosts(base, m.jumpInputs())
}

// setJumpMetadata repopulates the drafts from persisted metadata, leaving
// every password blank. It reports whether any bridge requires a password.
func (m *Model) setJumpMetadata(metas []opfapp.SSHJumpMetadata) bool {
	m.jumpDrafts = nil
	m.jumpIdx = 0
	needsPassword := false
	for _, meta := range metas {
		draft := newJumpDraft()
		draft.setMetadata(meta)
		m.jumpDrafts = append(m.jumpDrafts, draft)
		if meta.AuthMode != opfapp.SSHJumpAuthKey && meta.AuthMode != opfapp.SSHJumpAuthAgent {
			needsPassword = true
		}
	}
	return needsPassword
}

func (m *Model) removeJumpDraft(index int) {
	if index < 0 || index >= len(m.jumpDrafts) {
		return
	}
	m.jumpDrafts = append(m.jumpDrafts[:index], m.jumpDrafts[index+1:]...)
	if m.jumpIdx >= len(m.jumpDrafts) {
		m.jumpIdx = len(m.jumpDrafts) - 1
	}
	if m.jumpIdx < 0 {
		m.jumpIdx = 0
	}
}

func (m *Model) moveJumpDraft(index, delta int) {
	target := index + delta
	if index < 0 || index >= len(m.jumpDrafts) || target < 0 || target >= len(m.jumpDrafts) {
		return
	}
	m.jumpDrafts[index], m.jumpDrafts[target] = m.jumpDrafts[target], m.jumpDrafts[index]
	m.jumpIdx = target
}

func (m *Model) openJumpEdit(index int) {
	draft := newJumpDraft()
	if index >= 0 && index < len(m.jumpDrafts) {
		draft = m.jumpDrafts[index]
	}
	m.jumpEditIdx = index
	m.jumpHostEdit.SetValue(draft.host)
	m.jumpPortEdit.SetValue(draft.port)
	m.jumpUserEdit.SetValue(draft.user)
	m.jumpAuthEdit = draft.authMode
	m.jumpKeyEdit.SetValue(draft.keyPath)
	if m.jumpKeyPassphraseEdit != nil {
		m.jumpKeyPassphraseEdit.SetValue(draft.keyPassphrase)
	}
	m.jumpPasswordEdit.SetValue(draft.password)
	m.jumpKnownHostsEdit.SetValue(draft.knownHosts)
	m.errorMsg = ""
	m.screen = screenJumpEdit
	m.buildForm()
}

func (m *Model) saveJumpEdit() {
	passphrase := ""
	if m.jumpKeyPassphraseEdit != nil {
		passphrase = m.jumpKeyPassphraseEdit.Value()
	}
	draft := jumpDraft{
		host:          strings.TrimSpace(m.jumpHostEdit.Value()),
		port:          strings.TrimSpace(m.jumpPortEdit.Value()),
		user:          strings.TrimSpace(m.jumpUserEdit.Value()),
		authMode:      m.jumpAuthEdit,
		password:      m.jumpPasswordEdit.Value(),
		keyPath:       strings.TrimSpace(m.jumpKeyEdit.Value()),
		keyPassphrase: passphrase,
		knownHosts:    strings.TrimSpace(m.jumpKnownHostsEdit.Value()),
	}
	index := m.jumpEditIdx
	if index < 0 {
		index = len(m.jumpDrafts)
	}
	if err := opfapp.ValidateSSHJumpInput(index+1, draft.input()); err != nil {
		m.errorMsg = err.Error()
		return
	}
	if m.jumpEditIdx < 0 {
		m.jumpDrafts = append(m.jumpDrafts, draft)
		m.jumpIdx = len(m.jumpDrafts) - 1
	} else {
		m.jumpDrafts[m.jumpEditIdx] = draft
		m.jumpIdx = m.jumpEditIdx
	}
	m.errorMsg = ""
	m.screen = screenJumps
	m.buildForm()
}

func (m *Model) updateJumps(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenSSH
		m.buildForm()
	case "up", "k":
		if m.jumpIdx > 0 {
			m.jumpIdx--
		}
	case "down", "j":
		if m.jumpIdx < len(m.jumpDrafts)-1 {
			m.jumpIdx++
		}
	case "a":
		m.openJumpEdit(-1)
	case "e", "enter":
		if len(m.jumpDrafts) > 0 {
			m.openJumpEdit(m.jumpIdx)
		}
	case "d", "delete":
		m.removeJumpDraft(m.jumpIdx)
	case "K":
		m.moveJumpDraft(m.jumpIdx, -1)
	case "J":
		m.moveJumpDraft(m.jumpIdx, 1)
	}
	return m, nil
}
