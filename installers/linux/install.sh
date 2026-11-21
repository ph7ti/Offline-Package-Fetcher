#!/usr/bin/env bash
# OPF Linux installer (self-extracting payload created by build-linux-installer.sh).
#
# Four profiles:
#   1) bundle  GUI + TUI + CLI
#   2) gui     GUI only
#   3) tui     TUI only
#   4) cli     CLI only
#
# Non-interactive use:
#   OPF_INSTALL_PROFILE=bundle ./opf-installer-linux-amd64.run
#   OPF_INSTALL_PREFIX=/tmp/opf ./opf-installer-linux-amd64.run
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${OPF_INSTALL_PROFILE:-}"
PREFIX="${OPF_INSTALL_PREFIX:-}"

usage() {
	cat <<'EOF'
OPF installer

Options:
  bundle  install GUI, TUI and CLI
  gui     install GUI only
  tui     install TUI only
  cli     install CLI only
  --help  show this help

Environment:
  OPF_INSTALL_PROFILE   bundle|gui|tui|cli (skips the menu)
  OPF_INSTALL_PREFIX    installation prefix (default: /usr/local as root,
                        ~/.local otherwise)
EOF
}

choose_profile() {
	if [[ -n "${PROFILE}" ]]; then
		return
	fi
	if [[ ! -t 0 ]]; then
		echo "error: no interactive terminal; set OPF_INSTALL_PROFILE=bundle|gui|tui|cli" >&2
		exit 2
	fi
	local choice
	if command -v dialog >/dev/null 2>&1; then
		choice=$(dialog --stdout --title "OPF installer" --menu "Choose the installation profile:" 16 70 5 \
			1 "Bundle: GUI + TUI + CLI" \
			2 "GUI only" \
			3 "TUI only" \
			4 "CLI only" 3>&1 1>&2 2>&3) || exit 130
	elif command -v whiptail >/dev/null 2>&1; then
		choice=$(whiptail --title "OPF installer" --menu "Choose the installation profile:" 16 70 5 \
			1 "Bundle: GUI + TUI + CLI" \
			2 "GUI only" \
			3 "TUI only" \
			4 "CLI only" 3>&1 1>&2 2>&3) || exit 130
	else
		echo "OPF installer" >&2
		echo "  1) Bundle: GUI + TUI + CLI" >&2
		echo "  2) GUI only" >&2
		echo "  3) TUI only" >&2
		echo "  4) CLI only" >&2
		read -r -p "Profile [1-4]: " choice
	fi
	case "${choice}" in
		1) PROFILE=bundle ;;
		2) PROFILE=gui ;;
		3) PROFILE=tui ;;
		4) PROFILE=cli ;;
		*) echo "error: invalid profile" >&2; exit 2 ;;
	esac
}

for arg in "$@"; do
	case "${arg}" in
		--help|-h) usage; exit 0 ;;
		bundle|gui|tui|cli) PROFILE="${arg}" ;;
		*) echo "error: unknown argument: ${arg}" >&2; usage >&2; exit 2 ;;
	esac
done

choose_profile
case "${PROFILE}" in
	bundle|gui|tui|cli) ;;
	*) echo "error: invalid profile '${PROFILE}' (use bundle|gui|tui|cli)" >&2; exit 2 ;;
esac

if [[ -z "${PREFIX}" ]]; then
	if [[ "${EUID}" -eq 0 ]]; then
		PREFIX=/usr/local
	else
		PREFIX="${HOME}/.local"
	fi
fi
BINDIR="${PREFIX}/bin"

install -d -m 0755 "${BINDIR}"

install_gui() {
	install -m 0755 "${SCRIPT_DIR}/opf-gui" "${BINDIR}/opf-gui"
	if [[ "${EUID}" -eq 0 ]]; then
		APPDIR="${PREFIX}/share/applications"
	else
		APPDIR="${HOME}/.local/share/applications"
	fi
	install -d -m 0755 "${APPDIR}"
	cat >"${APPDIR}/opf.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Offline Package Fetcher
Comment=Offline Linux package fetcher
Exec=${BINDIR}/opf-gui
Terminal=false
Categories=Utility;PackageManager;
EOF
	chmod 0644 "${APPDIR}/opf.desktop"
}

install_tui() {
	install -m 0755 "${SCRIPT_DIR}/opf-tui" "${BINDIR}/opf-tui"
}

install_cli() {
	install -m 0755 "${SCRIPT_DIR}/opf-cli" "${BINDIR}/opf-cli"
}

case "${PROFILE}" in
	bundle) install_gui; install_tui; install_cli ;;
	gui) install_gui ;;
	tui) install_tui ;;
	cli) install_cli ;;
esac

cat <<EOF

OPF installed (${PROFILE}) in ${BINDIR}.
EOF
case "${PROFILE}" in
	bundle) printf 'Run: opf-gui (GUI), opf-tui (TUI), opf-cli (CLI)\n' ;;
	gui) printf 'Run: opf-gui\n' ;;
	tui) printf 'Run: opf-tui\n' ;;
	cli) printf 'Run: opf-cli\n' ;;
esac
if [[ ":${PATH}:" != *":${BINDIR}:"* ]]; then
	printf '\nAdd %s to PATH, for example:\n  export PATH="%s:$PATH"\n' "${BINDIR}" "${BINDIR}"
fi
