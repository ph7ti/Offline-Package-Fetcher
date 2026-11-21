package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	opfapp "opf/internal/app"
	"opf/internal/core"
)

const (
	repoKeySourceFile = "file"
	repoKeySourceURL  = "url"
	repoKeySourceNone = "none"
)

type repoDraft struct {
	name          string
	family        string
	url           string
	keySource     string
	keyValue      string
	caFile        string
	aptSuite      string
	aptComponents string
	aptFlat       bool
	insecureTLS   bool
}

func newRepoDraft(family string) repoDraft {
	return repoDraft{
		family:        family,
		keySource:     repoKeySourceFile,
		aptComponents: "main",
	}
}

func (d repoDraft) repository() core.PackageRepository {
	repo := core.PackageRepository{
		Name:                  strings.TrimSpace(d.name),
		Family:                d.family,
		URL:                   strings.TrimSpace(d.url),
		CAFile:                strings.TrimSpace(d.caFile),
		InsecureSkipTLSVerify: d.insecureTLS,
	}
	switch d.keySource {
	case repoKeySourceURL:
		repo.KeyURL = strings.TrimSpace(d.keyValue)
	case repoKeySourceNone:
		repo.InsecureSkipGPGCheck = true
	default:
		repo.KeyFile = strings.TrimSpace(d.keyValue)
	}
	if d.family == core.RepositoryFamilyAPT {
		repo.Flat = d.aptFlat
		if !repo.Flat {
			repo.Suite = strings.TrimSpace(d.aptSuite)
			repo.Components = splitRepoComponents(d.aptComponents)
		}
	}
	return repo
}

func (d *repoDraft) setRepository(repo core.PackageRepository) {
	d.name = repo.Name
	d.family = repo.Family
	d.url = repo.URL
	d.caFile = repo.CAFile
	d.insecureTLS = repo.InsecureSkipTLSVerify
	switch {
	case repo.InsecureSkipGPGCheck:
		d.keySource = repoKeySourceNone
		d.keyValue = ""
	case repo.KeyURL != "":
		d.keySource = repoKeySourceURL
		d.keyValue = repo.KeyURL
	default:
		d.keySource = repoKeySourceFile
		d.keyValue = repo.KeyFile
	}
	d.aptSuite = repo.Suite
	if len(repo.Components) > 0 {
		d.aptComponents = strings.Join(repo.Components, ",")
	} else {
		d.aptComponents = "main"
	}
	d.aptFlat = repo.Flat
}

func (d repoDraft) summary() string {
	url := strings.TrimSpace(d.url)
	if url == "" {
		url = "?"
	}
	key := "chave por arquivo"
	switch d.keySource {
	case repoKeySourceURL:
		key = "chave por URL"
	case repoKeySourceNone:
		key = "sem chave (inseguro)"
	}
	return fmt.Sprintf("%s • %s • %s", strings.TrimSpace(d.name), url, key)
}

func repoKeySourceDisplay(source string) string {
	switch source {
	case repoKeySourceURL:
		return "URL"
	case repoKeySourceNone:
		return "sem chave (inseguro)"
	default:
		return "arquivo"
	}
}

func splitRepoComponents(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (m *Model) targetFamily() (string, error) {
	return opfapp.TargetFamily(strings.ToLower(strings.TrimSpace(m.targetInput.Value())))
}

func (m *Model) repoFamilyOrApt() string {
	family, err := m.targetFamily()
	if err != nil {
		return core.RepositoryFamilyAPT
	}
	return family
}

func (m *Model) repoInputs() []core.PackageRepository {
	repos := make([]core.PackageRepository, 0, len(m.repoDrafts))
	for _, draft := range m.repoDrafts {
		repos = append(repos, draft.repository())
	}
	return repos
}

func (m *Model) buildRepositories() ([]core.PackageRepository, error) {
	family, err := m.targetFamily()
	if err != nil {
		return nil, err
	}
	repos, err := core.NormalizePackageRepositories(m.repoInputs(), family)
	if err != nil {
		return nil, fmt.Errorf("repositórios adicionais: %w", err)
	}
	return repos, nil
}

func (m *Model) setRepositories(repos []core.PackageRepository) {
	m.repoDrafts = nil
	m.repoIdx = 0
	for _, repo := range repos {
		family := repo.Family
		if family == "" {
			family = m.repoFamilyOrApt()
		}
		draft := newRepoDraft(family)
		draft.setRepository(repo)
		draft.family = family
		m.repoDrafts = append(m.repoDrafts, draft)
	}
}

func (m *Model) removeRepoDraft(index int) {
	if index < 0 || index >= len(m.repoDrafts) {
		return
	}
	m.repoDrafts = append(m.repoDrafts[:index], m.repoDrafts[index+1:]...)
	if m.repoIdx >= len(m.repoDrafts) {
		m.repoIdx = len(m.repoDrafts) - 1
	}
	if m.repoIdx < 0 {
		m.repoIdx = 0
	}
}

func (m *Model) openRepoEdit(index int) {
	draft := newRepoDraft(m.repoFamilyOrApt())
	if index >= 0 && index < len(m.repoDrafts) {
		draft = m.repoDrafts[index]
	}
	m.repoEditIdx = index
	m.repoFamilyEdit = draft.family
	m.repoNameEdit.SetValue(draft.name)
	m.repoURLEdit.SetValue(draft.url)
	m.repoKeySourceEdit = draft.keySource
	m.repoKeyValueEdit.SetValue(draft.keyValue)
	m.repoCAEdit.SetValue(draft.caFile)
	m.repoAPTSuiteEdit.SetValue(draft.aptSuite)
	m.repoAPTComponentsEdit.SetValue(draft.aptComponents)
	m.repoAPTFlatEdit = draft.aptFlat
	m.repoInsecureTLSEdit = draft.insecureTLS
	m.errorMsg = ""
	m.screen = screenRepoEdit
	m.buildForm()
}

func (m *Model) saveRepoEdit() {
	draft := repoDraft{
		name:          strings.TrimSpace(m.repoNameEdit.Value()),
		family:        m.repoFamilyEdit,
		url:           strings.TrimSpace(m.repoURLEdit.Value()),
		keySource:     m.repoKeySourceEdit,
		keyValue:      strings.TrimSpace(m.repoKeyValueEdit.Value()),
		caFile:        strings.TrimSpace(m.repoCAEdit.Value()),
		aptSuite:      strings.TrimSpace(m.repoAPTSuiteEdit.Value()),
		aptComponents: strings.TrimSpace(m.repoAPTComponentsEdit.Value()),
		aptFlat:       m.repoAPTFlatEdit,
		insecureTLS:   m.repoInsecureTLSEdit,
	}
	candidate := make([]repoDraft, len(m.repoDrafts))
	copy(candidate, m.repoDrafts)
	if m.repoEditIdx < 0 {
		candidate = append(candidate, draft)
	} else if m.repoEditIdx < len(candidate) {
		candidate[m.repoEditIdx] = draft
	}
	inputs := make([]core.PackageRepository, 0, len(candidate))
	for _, item := range candidate {
		inputs = append(inputs, item.repository())
	}
	family, err := m.targetFamily()
	if err == nil {
		_, err = core.NormalizePackageRepositories(inputs, family)
	}
	if err != nil {
		m.errorMsg = err.Error()
		return
	}
	m.repoDrafts = candidate
	if m.repoEditIdx < 0 {
		m.repoIdx = len(m.repoDrafts) - 1
	} else {
		m.repoIdx = m.repoEditIdx
	}
	m.errorMsg = ""
	m.screen = screenRepos
	m.buildForm()
}

func (m *Model) repoFamilyMismatch() bool {
	family, err := m.targetFamily()
	if err != nil {
		return false
	}
	for _, draft := range m.repoDrafts {
		if draft.family != family {
			return true
		}
	}
	return false
}

func (m *Model) updateRepos(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenConfig
		m.buildForm()
	case "up", "k":
		if m.repoIdx > 0 {
			m.repoIdx--
		}
	case "down", "j":
		if m.repoIdx < len(m.repoDrafts)-1 {
			m.repoIdx++
		}
	case "a":
		m.openRepoEdit(-1)
	case "e", "enter":
		if len(m.repoDrafts) > 0 {
			m.openRepoEdit(m.repoIdx)
		}
	case "d", "delete":
		m.removeRepoDraft(m.repoIdx)
	}
	return m, nil
}
