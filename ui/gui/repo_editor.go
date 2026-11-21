//go:build cgo

package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"opf/internal/core"
	"opf/internal/i18n"
)

// repoKeySources holds the Portuguese source labels used as internal values.
// repoKeySourceOptions renders the translated labels shown to the user.
var repoKeySources = []string{"Arquivo", "URL", "Sem chave (inseguro)"}

// repoKeySourceVariants lists the known translations for each key source so the
// semantic index survives an interface language change.
var repoKeySourceVariants = [][]string{
	{"Arquivo", "File", "Archivo"},
	{"URL"},
	{"Sem chave (inseguro)", "No key (insecure)", "Sin clave (inseguro)"},
}

func repoKeySourceOptions() []string {
	return []string{
		i18n.T(repoKeySources[0]),
		i18n.T(repoKeySources[1]),
		i18n.T(repoKeySources[2]),
	}
}

func repoKeySourceLabel(index int) string {
	if index < 0 || index >= len(repoKeySources) {
		index = 0
	}
	return i18n.T(repoKeySources[index])
}

func repoKeySourceIndex(selected string) int {
	selected = strings.TrimSpace(selected)
	for index := range repoKeySources {
		for _, variant := range repoKeySourceVariants[index] {
			if selected == variant || selected == i18n.T(repoKeySources[index]) {
				return index
			}
		}
	}
	return 0
}

type repoRow struct {
	family string

	name          *widget.Entry
	url           *widget.Entry
	keySource     *widget.Select
	keyValue      *widget.Entry
	caFile        *widget.Entry
	aptSuite      *widget.Entry
	aptComponents *widget.Entry
	aptFlat       *widget.Check
	insecureTLS   *widget.Check
}

func newRepoRow(family string) *repoRow {
	row := &repoRow{
		family:        family,
		name:          widget.NewEntry(),
		url:           widget.NewEntry(),
		keySource:     widget.NewSelect(repoKeySourceOptions(), nil),
		keyValue:      widget.NewEntry(),
		caFile:        widget.NewEntry(),
		aptSuite:      widget.NewEntry(),
		aptComponents: widget.NewEntry(),
		aptFlat:       widget.NewCheck(i18n.T("Repositório flat (sem suite/components)"), nil),
		insecureTLS:   widget.NewCheck(i18n.T("Ignorar verificação TLS (inseguro)"), nil),
	}
	row.keySource.PlaceHolder = i18n.T("Selecione a origem da chave")
	row.name.SetPlaceHolder(i18n.T("nome do repositório"))
	row.url.SetPlaceHolder(i18n.T("https://repo.interno/caminho"))
	row.keyValue.SetPlaceHolder(i18n.T("chave do repositório (arquivo ou URL)"))
	row.caFile.SetPlaceHolder(i18n.T("CA customizada (opcional)"))
	row.aptSuite.SetPlaceHolder(i18n.T("ex.: bookworm, jammy"))
	row.aptComponents.SetText("main")
	row.keySource.OnChanged = func(string) { row.syncKeySource() }
	row.aptFlat.OnChanged = func(bool) { row.syncAPTFields() }
	row.keySource.SetSelected(repoKeySourceLabel(0))
	row.syncKeySource()
	row.syncFamilyFields()
	return row
}

// refreshKeySourceOptions rebuilds the key-source labels in the active language
// while keeping the selected source.
func (r *repoRow) refreshKeySourceOptions() {
	index := repoKeySourceIndex(r.keySource.Selected)
	r.keySource.Options = repoKeySourceOptions()
	r.keySource.SetSelected(repoKeySourceLabel(index))
	r.keySource.Refresh()
}

func (r *repoRow) syncKeySource() {
	index := repoKeySourceIndex(r.keySource.Selected)
	if index == 2 {
		r.keyValue.Disable()
		return
	}
	r.keyValue.Enable()
	switch index {
	case 1:
		r.keyValue.SetPlaceHolder(i18n.T("https://repo.interno/chave.asc"))
	default:
		r.keyValue.SetPlaceHolder(i18n.T("caminho da chave no host"))
	}
}

func (r *repoRow) syncAPTFields() {
	apt := r.family == core.RepositoryFamilyAPT
	if !apt {
		r.aptSuite.Disable()
		r.aptComponents.Disable()
		r.aptFlat.Disable()
		return
	}
	if r.aptFlat.Checked {
		r.aptSuite.Disable()
		r.aptComponents.Disable()
		return
	}
	r.aptSuite.Enable()
	r.aptComponents.Enable()
	r.aptFlat.Enable()
}

func (r *repoRow) syncFamilyFields() {
	r.syncAPTFields()
	if r.family == core.RepositoryFamilyAPK {
		r.insecureTLS.SetChecked(false)
		r.insecureTLS.Disable()
	} else {
		r.insecureTLS.Enable()
	}
}

func (r *repoRow) repository() core.PackageRepository {
	repo := core.PackageRepository{
		Name:                  strings.TrimSpace(r.name.Text),
		Family:                r.family,
		URL:                   strings.TrimSpace(r.url.Text),
		CAFile:                strings.TrimSpace(r.caFile.Text),
		InsecureSkipTLSVerify: r.insecureTLS.Checked,
	}
	switch repoKeySourceIndex(r.keySource.Selected) {
	case 1:
		repo.KeyURL = strings.TrimSpace(r.keyValue.Text)
	case 2:
		repo.InsecureSkipGPGCheck = true
	default:
		repo.KeyFile = strings.TrimSpace(r.keyValue.Text)
	}
	if r.family == core.RepositoryFamilyAPT {
		repo.Flat = r.aptFlat.Checked
		if !repo.Flat {
			repo.Suite = strings.TrimSpace(r.aptSuite.Text)
			repo.Components = splitRepoComponents(r.aptComponents.Text)
		}
	}
	return repo
}

func (r *repoRow) setRepository(repo core.PackageRepository) {
	r.family = repo.Family
	r.name.SetText(repo.Name)
	r.url.SetText(repo.URL)
	r.caFile.SetText(repo.CAFile)
	r.insecureTLS.SetChecked(repo.InsecureSkipTLSVerify)
	switch {
	case repo.InsecureSkipGPGCheck:
		r.keySource.SetSelected(repoKeySourceLabel(2))
		r.keyValue.SetText("")
	case repo.KeyURL != "":
		r.keySource.SetSelected(repoKeySourceLabel(1))
		r.keyValue.SetText(repo.KeyURL)
	default:
		r.keySource.SetSelected(repoKeySourceLabel(0))
		r.keyValue.SetText(repo.KeyFile)
	}
	r.aptSuite.SetText(repo.Suite)
	if len(repo.Components) > 0 {
		r.aptComponents.SetText(strings.Join(repo.Components, ","))
	} else {
		r.aptComponents.SetText("main")
	}
	r.aptFlat.SetChecked(repo.Flat)
	r.syncKeySource()
	r.syncFamilyFields()
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

func (u *UI) repositoryFamilyOrApt() string {
	family, err := u.targetFamily()
	if err != nil {
		return core.RepositoryFamilyAPT
	}
	return family
}

func (u *UI) addRepoRow() {
	family := u.repositoryFamilyOrApt()
	row := newRepoRow(family)
	row.name.SetText(i18n.T("Repositório %d", len(u.repoRows)+1))
	u.repoRows = append(u.repoRows, row)
	u.refreshRepoList()
}

func (u *UI) removeRepoRow(index int) {
	if index < 0 || index >= len(u.repoRows) {
		return
	}
	u.repoRows = append(u.repoRows[:index], u.repoRows[index+1:]...)
	u.refreshRepoList()
}

func (u *UI) clearRepoRows() {
	u.repoRows = nil
	u.refreshRepoList()
}

func (u *UI) setRepositories(repos []core.PackageRepository) {
	u.repoRows = nil
	for _, repo := range repos {
		family := repo.Family
		if family == "" {
			family = u.repositoryFamilyOrApt()
		}
		row := newRepoRow(family)
		row.setRepository(repo)
		row.family = family
		row.syncFamilyFields()
		u.repoRows = append(u.repoRows, row)
	}
}

func (u *UI) repositoryInputs() []core.PackageRepository {
	repos := make([]core.PackageRepository, 0, len(u.repoRows))
	for _, row := range u.repoRows {
		repos = append(repos, row.repository())
	}
	return repos
}

func (u *UI) buildRepositories() ([]core.PackageRepository, error) {
	family, err := u.targetFamily()
	if err != nil {
		return nil, err
	}
	repos, err := core.NormalizePackageRepositories(u.repositoryInputs(), family)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("repositórios adicionais"), err)
	}
	return repos, nil
}

func (u *UI) repoFamilyMismatch() bool {
	family, err := u.targetFamily()
	if err != nil {
		return false
	}
	for _, row := range u.repoRows {
		if row.family != family {
			return true
		}
	}
	return false
}

func (u *UI) refreshRepoList() {
	if u.repoList == nil {
		return
	}
	objects := make([]fyne.CanvasObject, 0, len(u.repoRows)+2)
	if u.repoFamilyMismatch() {
		objects = append(objects, u.banner(i18n.T("Há repositórios configurados para outra família de destino. Remova-os ou volte ao alvo correto antes de continuar."), toneError), vSpace(8))
	}
	if len(u.repoRows) == 0 {
		objects = append(objects, u.banner(i18n.T("Nenhum repositório adicional. O download usará apenas os repositórios oficiais."), toneInfo))
	} else {
		for i, row := range u.repoRows {
			row.refreshKeySourceOptions()
			if i > 0 {
				objects = append(objects, vSpace(10))
			}
			objects = append(objects, u.repoRowCard(row, i))
		}
	}
	u.repoList.Objects = objects
	u.repoList.Refresh()
}

func (u *UI) repoRowCard(row *repoRow, index int) fyne.CanvasObject {
	remove := u.ghostButton(i18n.T("Remover"), theme.DeleteIcon(), func() { u.removeRepoRow(index) })
	header := container.NewHBox(
		textLine(i18n.T("Repositório %d", index+1), 13, true, theme.Color(theme.ColorNameForeground)),
		layout.NewSpacer(),
		remove,
	)
	keyBrowse := u.ghostButton(i18n.T("Procurar"), theme.FolderOpenIcon(), func() {
		row.keySource.SetSelected(repoKeySourceLabel(0))
		u.pickRepositoryFile(row.keyValue)
	})
	keyRow := container.NewBorder(nil, nil, nil, keyBrowse, row.keyValue)
	caBrowse := u.ghostButton(i18n.T("Procurar"), theme.FolderOpenIcon(), func() { u.pickRepositoryFile(row.caFile) })
	caRow := container.NewBorder(nil, nil, nil, caBrowse, row.caFile)
	items := []*widget.FormItem{
		widget.NewFormItem(i18n.T("Nome"), row.name),
		widget.NewFormItem(i18n.T("URL"), row.url),
		widget.NewFormItem(i18n.T("Chave"), row.keySource),
		widget.NewFormItem(i18n.T("Chave (arquivo/URL)"), keyRow),
		widget.NewFormItem(i18n.T("CA customizada"), caRow),
		widget.NewFormItem(i18n.T("TLS"), row.insecureTLS),
	}
	if row.family == core.RepositoryFamilyAPT {
		items = append(items,
			widget.NewFormItem(i18n.T("Suite APT"), row.aptSuite),
			widget.NewFormItem(i18n.T("Components APT"), row.aptComponents),
			widget.NewFormItem("", row.aptFlat),
		)
	}
	note := u.muted(i18n.T("Repositório usado somente no ambiente de download; a lista é aditiva e não substitui os repositórios oficiais."), theme.SizeNameCaptionText)
	if row.family == core.RepositoryFamilyAPK {
		note = u.muted(i18n.T("APK aceita CA customizada, mas não insecure TLS. Sem chave, use o opt-in inseguro de GPG."), theme.SizeNameCaptionText)
	}
	return u.cardShell(theme.Color(colorCardBackground), theme.Color(colorBorder), 1, container.NewVBox(
		header,
		vSpace(6),
		widget.NewForm(items...),
		vSpace(6),
		note,
	))
}

func (u *UI) pickRepositoryFile(entry *widget.Entry) {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			u.showError(err)
			return
		}
		if reader == nil {
			return
		}
		path := filepath.FromSlash(reader.URI().Path())
		if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		_ = reader.Close()
		entry.SetText(path)
	}, u.window)
}
