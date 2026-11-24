#!/usr/bin/env bash
# Build an OPF macOS installer (.pkg) with four choices.
#
# Usage: build-macos-installer.sh <amd64|arm64> <payload-dir> <output.pkg> [version]
# Payload directory must contain: opf-cli, opf-tui, opf-gui.
set -euo pipefail

arch="${1:-}"
payload="${2:-}"
out="${3:-}"
version="${4:-0.4.1}"
if [[ -z "${arch}" || -z "${payload}" || -z "${out}" ]]; then
	echo "usage: $0 <amd64|arm64> <payload-dir> <output.pkg> [version]" >&2
	exit 2
fi
for binary in opf-cli opf-tui opf-gui; do
	[[ -x "${payload}/${binary}" ]] || { echo "error: missing executable ${payload}/${binary}" >&2; exit 2; }
done

case "${arch}" in
	amd64) host_arch=x86_64 ;;
	arm64) host_arch=arm64 ;;
	*) echo "error: arch must be amd64 or arm64" >&2; exit 2 ;;
esac

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
mkdir -p "${work}/root-cli/usr/local/bin" "${work}/root-tui/usr/local/bin"
mkdir -p "${work}/root-gui/Applications/OPF.app/Contents/MacOS" "${work}/root-gui/Applications/OPF.app/Contents/Resources"
install -m 0755 "${payload}/opf-cli" "${work}/root-cli/usr/local/bin/opf-cli"
install -m 0755 "${payload}/opf-tui" "${work}/root-tui/usr/local/bin/opf-tui"
install -m 0755 "${payload}/opf-gui" "${work}/root-gui/Applications/OPF.app/Contents/MacOS/opf-gui"
install -m 0644 "$(dirname "$0")/../../images/icon.icns" "${work}/root-gui/Applications/OPF.app/Contents/Resources/opf.icns"

cat >"${work}/root-gui/Applications/OPF.app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDisplayName</key><string>OPF</string>
  <key>CFBundleExecutable</key><string>opf-gui</string>
  <key>CFBundleIconFile</key><string>opf.icns</string>
  <key>CFBundleIdentifier</key><string>org.opf.gui</string>
  <key>CFBundleName</key><string>OPF</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF

mkdir -p "${work}/scripts"
install -m 0755 "$(dirname "$0")/../../installers/macos/postinstall" "${work}/scripts/postinstall"

pkgbuild --root "${work}/root-cli" --scripts "${work}/scripts" --identifier org.opf.cli --version "${version}" --install-location / "${work}/cli.pkg"
pkgbuild --root "${work}/root-tui" --scripts "${work}/scripts" --identifier org.opf.tui --version "${version}" --install-location / "${work}/tui.pkg"
pkgbuild --root "${work}/root-gui" --scripts "${work}/scripts" --identifier org.opf.gui --version "${version}" --install-location / "${work}/gui.pkg"

sed -e "s/@ARCH@/${host_arch}/g" -e "s/@VERSION@/${version}/g" \
	"$(dirname "$0")/../../installers/macos/distribution.xml" >"${work}/distribution.xml"
mkdir -p "$(dirname "${out}")"
productbuild --distribution "${work}/distribution.xml" --package-path "${work}" "${out}"
echo "built ${out}"
