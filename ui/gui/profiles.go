//go:build cgo

package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2/dialog"

	opfapp "opf/internal/app"
	"opf/internal/config"
	"opf/internal/i18n"
	"opf/internal/transfer"
)

func (u *UI) effectiveProfilesPath() string {
	if strings.TrimSpace(u.profilesPath) != "" {
		return u.profilesPath
	}
	p, err := config.DefaultProfilesPath()
	if err != nil {
		return ""
	}
	return p
}

func (u *UI) refreshProfiles() {
	if u.profileSelect == nil {
		return
	}
	profiles, err := config.LoadProfiles(u.effectiveProfilesPath())
	if err != nil {
		u.profileSelect.Options = nil
		u.profileSelect.Refresh()
		return
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	u.profileSelect.Options = names
	u.profileSelect.Refresh()
}

func (u *UI) loadSelectedProfile() {
	name := strings.TrimSpace(u.profileSelect.Selected)
	if name == "" {
		u.showError(fmt.Errorf("%s", i18n.T("selecione um perfil")))
		return
	}
	p, err := config.GetProfile(u.effectiveProfilesPath(), name)
	if err != nil {
		u.showError(err)
		return
	}
	u.applyProfile(*p)
	if u.profileNameEntry != nil {
		u.profileNameEntry.SetText(p.Name)
	}
	dialog.ShowInformation(i18n.T("Perfis salvos"), i18n.T("Perfil carregado: %s", p.Name), u.window)
}

func (u *UI) applyProfile(p config.Profile) {
	if p.TargetOS != "" {
		switch p.TargetOS {
		case "debian":
			u.targetSelect.SetSelected("Debian")
		case "centos":
			u.targetSelect.SetSelected("CentOS")
		case "rockylinux":
			u.targetSelect.SetSelected("Rocky Linux")
		case "almalinux":
			u.targetSelect.SetSelected("AlmaLinux")
		case "fedora":
			u.targetSelect.SetSelected("Fedora")
		case "rhel":
			u.targetSelect.SetSelected("RHEL")
		case "alpine":
			u.targetSelect.SetSelected("Alpine")
		default:
			u.targetSelect.SetSelected("Ubuntu")
		}
	}
	if p.TargetVersion != "" {
		u.versionEntry.SetText(p.TargetVersion)
	}
	if p.TargetArch != "" {
		u.archEntry.SetText(p.TargetArch)
	}
	if p.FetchMethod != "" {
		u.methodSelect.Options = methodOptions()
		u.methodSelect.SetSelected(methodLabel(p.FetchMethod))
		u.methodSelect.Refresh()
	}
	u.imageEntry.SetText(p.Image)
	u.wslEntry.SetText(p.WSLDistro)
	u.remoteDir.SetText(p.RemoteDir)
	u.optionalCheck.SetChecked(p.IncludeOptional)
	u.forceCheck.SetChecked(p.ForceReinstall)
	if u.offlineBundleCheck != nil {
		u.offlineBundleCheck.SetChecked(p.OfflineBundle)
	}
	if len(p.Packages) > 0 {
		u.pkgEntry.SetText(strings.Join(p.Packages, "\n"))
	}
	u.setRepositories(p.Repositories)
	u.refreshRepoList()
	if p.SSHHost != "" {
		u.sshEnabled.SetChecked(true)
		u.sshHost.SetText(p.SSHHost)
		u.sshUser.SetText(p.SSHUser)
		if p.SSHPort > 0 {
			u.sshPort.SetText(strconv.Itoa(p.SSHPort))
		}
		u.sshKey.SetText(p.SSHKeyPath)
		if u.sshUseAgent != nil {
			u.sshUseAgent.SetChecked(p.SSHUseAgent)
		}
		u.knownHosts.SetText(p.SSHKnownHosts)
	}
	if len(p.SSHJumps) > 0 {
		metas := make([]opfapp.SSHJumpMetadata, 0, len(p.SSHJumps))
		for _, j := range p.SSHJumps {
			metas = append(metas, opfapp.SSHJumpMetadata{
				Host:       j.Host,
				Port:       j.Port,
				User:       j.User,
				AuthMode:   j.AuthMode,
				KeyPath:    j.KeyPath,
				KnownHosts: j.KnownHosts,
			})
		}
		u.setJumpMetadata(metas)
		u.refreshJumpList()
	}
	u.manualTarget = true
}

func (u *UI) saveCurrentProfile() {
	name := strings.TrimSpace(u.profileNameEntry.Text)
	if name == "" {
		u.showError(fmt.Errorf("%s", i18n.T("informe um nome para o perfil")))
		return
	}
	port, _ := strconv.Atoi(strings.TrimSpace(u.sshPort.Text))
	if port <= 0 {
		port = 22
	}
	jumps := make([]config.ProfileJump, 0, len(u.jumpRows))
	for _, row := range u.jumpRows {
		meta := row.metadata()
		jumps = append(jumps, config.ProfileJump{
			Host:       meta.Host,
			Port:       meta.Port,
			User:       meta.User,
			AuthMode:   meta.AuthMode,
			KeyPath:    meta.KeyPath,
			KnownHosts: meta.KnownHosts,
		})
	}
	repos := u.repositoryInputs()
	profile := config.Profile{
		Name:            name,
		TargetOS:        u.targetOS(),
		TargetVersion:   strings.TrimSpace(u.versionEntry.Text),
		TargetArch:      strings.TrimSpace(u.archEntry.Text),
		FetchMethod:     u.fetchMethod(),
		Image:           strings.TrimSpace(u.imageEntry.Text),
		WSLDistro:       strings.TrimSpace(u.wslEntry.Text),
		RemoteDir:       strings.TrimSpace(u.remoteDir.Text),
		Packages:        u.packages(),
		IncludeOptional: u.optionalCheck.Checked,
		ForceReinstall:  u.forceCheck.Checked,
		OfflineBundle:   u.offlineBundleCheck != nil && u.offlineBundleCheck.Checked,
		Repositories:    repos,
		SSHHost:         strings.TrimSpace(u.sshHost.Text),
		SSHPort:         port,
		SSHUser:         strings.TrimSpace(u.sshUser.Text),
		SSHKeyPath:      strings.TrimSpace(u.sshKey.Text),
		SSHUseAgent:     u.sshUseAgent != nil && u.sshUseAgent.Checked,
		SSHKnownHosts:   strings.TrimSpace(u.knownHosts.Text),
		SSHJumps:        jumps,
	}
	if err := config.UpsertProfile(u.effectiveProfilesPath(), profile); err != nil {
		u.showError(err)
		return
	}
	u.refreshProfiles()
	u.profileSelect.SetSelected(name)
	dialog.ShowInformation(i18n.T("Perfis salvos"), i18n.T("Perfil salvo: %s", name), u.window)
}

func (u *UI) deleteSelectedProfile() {
	name := strings.TrimSpace(u.profileSelect.Selected)
	if name == "" {
		u.showError(fmt.Errorf("%s", i18n.T("selecione um perfil")))
		return
	}
	if err := config.DeleteProfile(u.effectiveProfilesPath(), name); err != nil {
		u.showError(err)
		return
	}
	u.profileSelect.ClearSelected()
	u.refreshProfiles()
	dialog.ShowInformation(i18n.T("Perfis salvos"), i18n.T("Perfil removido: %s", name), u.window)
}

func (u *UI) importSSHConfigAlias() {
	alias := strings.TrimSpace(u.sshConfigAlias.Text)
	if alias == "" {
		u.showError(fmt.Errorf("%s", i18n.T("informe um alias de ~/.ssh/config")))
		return
	}
	resolved, err := transfer.ResolveSSHConfigAlias("", alias)
	if err != nil {
		u.showError(err)
		return
	}
	u.sshEnabled.SetChecked(true)
	if resolved.Host != "" {
		u.sshHost.SetText(resolved.Host)
	}
	if resolved.Port > 0 {
		u.sshPort.SetText(strconv.Itoa(resolved.Port))
	}
	if resolved.User != "" {
		u.sshUser.SetText(resolved.User)
	}
	if resolved.KeyPath != "" {
		u.sshKey.SetText(resolved.KeyPath)
	}
	if resolved.KnownHosts != "" {
		u.knownHosts.SetText(resolved.KnownHosts)
	}
	if len(resolved.JumpHosts) > 0 {
		metas := make([]opfapp.SSHJumpMetadata, 0, len(resolved.JumpHosts))
		for _, j := range resolved.JumpHosts {
			mode := opfapp.SSHJumpAuthAgent
			if j.KeyPath != "" {
				mode = opfapp.SSHJumpAuthKey
			}
			metas = append(metas, opfapp.SSHJumpMetadata{
				Host:       j.Host,
				Port:       j.Port,
				User:       j.User,
				AuthMode:   mode,
				KeyPath:    j.KeyPath,
				KnownHosts: j.KnownHosts,
			})
		}
		u.setJumpMetadata(metas)
		u.refreshJumpList()
	}
	dialog.ShowInformation("SSH", i18n.T("Alias SSH importado: %s", alias), u.window)
}
