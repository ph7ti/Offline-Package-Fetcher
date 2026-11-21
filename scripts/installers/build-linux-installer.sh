#!/usr/bin/env bash
# Build an OPF Linux self-extracting installer.
#
# Usage: build-linux-installer.sh <amd64|arm64> <payload-dir> <output-file>
# Payload directory must contain: opf-cli, opf-tui, opf-gui (executable).
set -euo pipefail

arch="${1:-}"
payload="${2:-}"
out="${3:-}"
if [[ -z "${arch}" || -z "${payload}" || -z "${out}" ]]; then
	echo "usage: $0 <amd64|arm64> <payload-dir> <output-file>" >&2
	exit 2
fi
if [[ ! -d "${payload}" ]]; then
	echo "error: payload directory not found: ${payload}" >&2
	exit 2
fi
for binary in opf-cli opf-tui opf-gui; do
	if [[ ! -x "${payload}/${binary}" ]]; then
		echo "error: missing executable ${payload}/${binary}" >&2
		exit 2
	fi
done
command -v makeself >/dev/null 2>&1 || {
	echo "error: makeself not found (apt-get install makeself)" >&2
	exit 2
}

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
mkdir -p "${work}/payload"
cp "${payload}/opf-cli" "${payload}/opf-tui" "${payload}/opf-gui" "${work}/payload/"
cp "$(dirname "$0")/../../installers/linux/install.sh" "${work}/payload/install.sh"
chmod 0755 "${work}/payload/install.sh"
mkdir -p "$(dirname "${out}")"

makeself --gzip "${work}/payload" "${out}" "OPF installer (${arch})" ./install.sh
chmod 0755 "${out}"
echo "built ${out}"
