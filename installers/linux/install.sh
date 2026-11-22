#!/usr/bin/env bash
# OPF Linux installer (self-extracting payload created by build-linux-installer.sh).
#
# Four profiles: bundle, gui, tui, cli.
# Non-interactive use:
#   OPF_INSTALL_PROFILE=bundle OPF_INSTALL_PREFIX=/opt/opf ./opf-installer-linux-amd64.run
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="${OPF_INSTALL_PROFILE:-}"
PREFIX="${OPF_INSTALL_PREFIX:-}"
NO_SHORTCUT="${OPF_NO_SHORTCUT:-0}"
NO_LAUNCH="${OPF_NO_LAUNCH:-0}"

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
  OPF_INSTALL_PROFILE   bundle|gui|tui|cli (skips the profile menu)
  OPF_INSTALL_PREFIX    installation prefix (default: /usr/local as root,
                        ~/.local otherwise)
  OPF_NO_SHORTCUT=1     do not create the desktop entry
  OPF_NO_LAUNCH=1       do not offer to open the software
EOF
}

box() {
	local line
	echo "+----------------------------------------------------------------+"
	for line in "$@"; do
		printf '| %-62s |\n' "${line:0:62}"
	done
	echo "+----------------------------------------------------------------+"
}

ask() {
	local label="$1" default="$2" value
	printf '%s [%s]: ' "${label}" "${default}" >&2
	read -r value || value=""
	printf '%s' "${value:-$default}"
}

ask_yes_no() {
	local label="$1" default="$2" value
	local hint="Y/n"
	[[ "${default}" == "n" ]] && hint="y/N"
	while true; do
		printf '%s [%s]: ' "${label}" "${hint}" >&2
		read -r value || value=""
		value="${value:-$default}"
		case "${value}" in
			y|Y|yes|YES|s|S|sim|SIM) return 0 ;;
			n|N|no|NO|nao|NAO|não|NÃO) return 1 ;;
			*) echo "  Responda Y (sim) ou N (não)." ;;
		esac
	done
}

for arg in "$@"; do
	case "${arg}" in
		--help|-h) usage; exit 0 ;;
		bundle|gui|tui|cli) PROFILE="${arg}" ;;
		--no-shortcut) NO_SHORTCUT=1 ;;
		--no-launch) NO_LAUNCH=1 ;;
		*) echo "error: unknown argument: ${arg}" >&2; usage >&2; exit 2 ;;
	esac
done

box "Offline Package Fetcher (OPF) - Installer" "Version 0.2.7" "" \
	"This wizard installs OPF on your computer." \
	"You can change the folder and choose what to install."

if [[ -z "${PROFILE}" ]]; then
	if [[ ! -t 0 ]]; then
		echo "error: no interactive terminal; set OPF_INSTALL_PROFILE=bundle|gui|tui|cli" >&2
		exit 2
	fi
	echo
	echo "Step 1 of 4 - Choose what to install:"
	echo "  1) Bundle: GUI + TUI + CLI"
	echo "  2) GUI only"
	echo "  3) TUI only"
	echo "  4) CLI only"
	while true; do
		choice="$(ask "Profile" "1")"
		case "${choice}" in
			1) PROFILE=bundle; break ;;
			2) PROFILE=gui; break ;;
			3) PROFILE=tui; break ;;
			4) PROFILE=cli; break ;;
			*) echo "  Digite 1, 2, 3 ou 4." ;;
		esac
	done
fi
case "${PROFILE}" in bundle|gui|tui|cli) ;; *) echo "error: invalid profile '${PROFILE}'" >&2; exit 2 ;; esac

if [[ -z "${PREFIX}" ]]; then
	if [[ "${EUID}" -eq 0 ]]; then PREFIX=/usr/local; else PREFIX="${HOME}/.local"; fi
fi
if [[ -t 0 ]]; then
	echo
	echo "Step 2 of 4 - Installation folder:"
	printf '  Default: %s\n' "${PREFIX}"
	PREFIX="$(ask "Install folder" "${PREFIX}")"
fi
BINDIR="${PREFIX}/bin"
if ! install -d -m 0755 "${BINDIR}" 2>/dev/null; then
	echo "error: cannot create ${BINDIR}; run as root or choose a folder in your home." >&2
	exit 1
fi

SHORTCUT=0
if [[ "${PROFILE}" == "bundle" || "${PROFILE}" == "gui" ]]; then
	if [[ "${NO_SHORTCUT}" == "1" ]]; then
		SHORTCUT=0
	elif [[ -t 0 ]]; then
		echo
		echo "Step 3 of 4 - Shortcuts:"
		if ask_yes_no "Create a desktop entry for OPF?" "y"; then SHORTCUT=1; fi
	else
		SHORTCUT=1
	fi
fi

echo
echo "Installing OPF..."
echo "  [1/4] Installing ${PROFILE}"
install_gui() {
	install -m 0755 "${SCRIPT_DIR}/opf-gui" "${BINDIR}/opf-gui"
	if [[ "${SHORTCUT}" -eq 1 ]]; then
		if [[ "${EUID}" -eq 0 ]]; then
			APPDIR="/usr/share/applications"
			ICONDIR="/usr/share/icons/hicolor/256x256/apps"
		else
			APPDIR="${HOME}/.local/share/applications"
			ICONDIR="${HOME}/.local/share/icons/hicolor/256x256/apps"
		fi
		install -d -m 0755 "${APPDIR}" "${ICONDIR}"
		install -m 0644 "${SCRIPT_DIR}/opf.png" "${ICONDIR}/opf.png"
		cat >"${APPDIR}/opf.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Offline Package Fetcher
Comment=Offline Linux package fetcher
Exec=${BINDIR}/opf-gui
Icon=opf
Terminal=false
Categories=Utility;PackageManager;
EOF
		chmod 0644 "${APPDIR}/opf.desktop"
		echo "        Desktop entry created."
	fi
}
install_tui() { install -m 0755 "${SCRIPT_DIR}/opf-tui" "${BINDIR}/opf-tui"; }
install_cli() { install -m 0755 "${SCRIPT_DIR}/opf-cli" "${BINDIR}/opf-cli"; }
case "${PROFILE}" in
	bundle) install_gui; install_tui; install_cli ;;
	gui) install_gui ;;
	tui) install_tui ;;
	cli) install_cli ;;
esac

echo "  [2/4] Updating your environment"
if [[ ":${PATH}:" != *":${BINDIR}:"* ]]; then
	echo "        Add ${BINDIR} to PATH, for example:"
	echo "          export PATH=\"${BINDIR}:\$PATH\""
else
	echo "        ${BINDIR} is already in PATH."
fi

echo "  [3/4] Creating the local database"
DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
DB="${DATA_HOME}/OPF/data/opf.db"
mkdir -p "$(dirname "${DB}")"
: >>"${DB}"
echo "        ${DB}"

if [[ "${SHORTCUT}" -eq 1 ]]; then echo "  [4/4] Shortcut created"; else echo "  [4/4] Shortcut skipped"; fi

echo
box "Installation completed successfully." "" \
	"Folder:   ${BINDIR}" "Database: ${DB}" "Profile:  ${PROFILE}"
if [[ "${NO_LAUNCH}" != "1" && -t 0 ]]; then
	echo
	echo "Step 4 of 4 - Open OPF:"
	options=()
	if [[ "${PROFILE}" == "bundle" || "${PROFILE}" == "gui" ]]; then options+=("Open the GUI"); fi
	if [[ "${PROFILE}" == "bundle" || "${PROFILE}" == "tui" ]]; then options+=("Open the TUI in this terminal"); fi
	options+=("Finish without opening")
	for i in "${!options[@]}"; do printf '  %d) %s\n' "$((i+1))" "${options[$i]}"; done
	choice="$(ask "Option" "${#options[@]}")"
	case "${options[$((choice-1))]:-}" in
		"Open the GUI") nohup "${BINDIR}/opf-gui" >/dev/null 2>&1 & ;;
		"Open the TUI in this terminal") exec "${BINDIR}/opf-tui" ;;
	esac
fi
