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
)

var repoKeySources = []string{"Arquivo", "URL", "Sem chave (inseguro)"}

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
		keySource:     widget.NewSelect(repoKeySources, nil),
		keyValue:      widget.NewEntry(),
		caFile:        widget.NewEntry(),
		aptSuite:      widget.NewEntry(),
		aptComponents: widget.NewEntry(),
		aptFlat:       widget.NewCheck("Repositório flat (sem suite/components)", nil),
		insecureTLS:   widget.NewCheck("Ignorar verificação TLS (inseguro)", nil),
	}
	row.name.SetPlaceHolder("nome do repositório")
	row.url.SetPlaceHolder("https://repo.interno/caminho")
	row.keyValue.SetPlaceHolder("chave do repositório (arquivo ou URL)")
	row.caFile.SetPlaceHolder("CA customizada (opcional)")
	row.aptSuite.SetPlaceHolder("ex.: bookworm, jammy")
	row.aptComponents.SetText("main")
	row.keySource.OnChanged = func(string) { row.syncKeySource() }
	row.aptFlat.OnChanged = func(bool) { row.syncAPTFields() }
	row.keySource.SetSelected(repoKeySources[0])
	row.syncKeySource()
	row.syncFamilyFields()
	return row
}

func (r *repoRow) syncKeySource() {
	none := r.keySource.Selected == repoKeySources[2]
	if none {
		r.keyValue.Disable()
		return
	}
	r.keyValue.Enable()
	switch r.keySource.Selected {
	case repoKeySources[1]:
		r.keyValue.SetPlaceHolder("https://repo.interno/chave.asc")
	default:
		r.keyValue.SetPlaceHolder("caminho da chave no host")
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
	switch r.keySource.Selected {
	case repoKeySources[1]:
		repo.KeyURL = strings.TrimSpace(r.keyValue.Text)
	case repoKeySources[2]:
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
		r.keySource.SetSelected(repoKeySources[2])
		r.keyValue.SetText("")
	case repo.KeyURL != "":
		r.keySource.SetSelected(repoKeySources[1])
		r.keyValue.SetText(repo.KeyURL)
	default:
		r.keySource.SetSelected(repoKeySources[0])
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
	row.name.SetText(fmt.Sprintf("Repositório %d", len(u.repoRows)+1))
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
		return nil, fmt.Errorf("repositórios adicionais: %w", err)
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
		objects = append(objects, u.banner("Há repositórios configurados para outra família de destino. Remova-os ou volte ao alvo correto antes de continuar.", toneError), vSpace(8))
	}
	if len(u.repoRows) == 0 {
		objects = append(objects, u.banner("Nenhum repositório adicional. O download usará apenas os repositórios oficiais.", toneInfo))
	} else {
		for i, row := range u.repoRows {
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
	remove := u.ghostButton("Remover", theme.DeleteIcon(), func() { u.removeRepoRow(index) })
	header := container.NewHBox(
		textLine(fmt.Sprintf("Repositório %d", index+1), 13, true, theme.Color(theme.ColorNameForeground)),
		layout.NewSpacer(),
		remove,
	)
	keyBrowse := u.ghostButton("Procurar", theme.FolderOpenIcon(), func() {
		row.keySource.SetSelected(repoKeySources[0])
		u.pickRepositoryFile(row.keyValue)
	})
	keyRow := container.NewBorder(nil, nil, nil, keyBrowse, row.keyValue)
	caBrowse := u.ghostButton("Procurar", theme.FolderOpenIcon(), func() { u.pickRepositoryFile(row.caFile) })
	caRow := container.NewBorder(nil, nil, nil, caBrowse, row.caFile)
	items := []*widget.FormItem{
		widget.NewFormItem("Nome", row.name),
		widget.NewFormItem("URL", row.url),
		widget.NewFormItem("Chave", row.keySource),
		widget.NewFormItem("Chave (arquivo/URL)", keyRow),
		widget.NewFormItem("CA customizada", caRow),
		widget.NewFormItem("TLS", row.insecureTLS),
	}
	if row.family == core.RepositoryFamilyAPT {
		items = append(items,
			widget.NewFormItem("Suite APT", row.aptSuite),
			widget.NewFormItem("Components APT", row.aptComponents),
			widget.NewFormItem("", row.aptFlat),
		)
	}
	note := u.muted("Repositório usado somente no ambiente de download; a lista é aditiva e não substitui os repositórios oficiais.", theme.SizeNameCaptionText)
	if row.family == core.RepositoryFamilyAPK {
		note = u.muted("APK aceita CA customizada, mas não insecure TLS. Sem chave, use o opt-in inseguro de GPG.", theme.SizeNameCaptionText)
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
