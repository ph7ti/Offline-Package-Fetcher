#!/usr/bin/env bash
# Reproducible OPF security scan (Linux/macOS).
#
# Runs the security regression suite plus the same scanners used to produce
# docs/SECURITY.md. Fail-fast: the first failing gate aborts with a non-zero
# exit status. Reports go to bin/security/ (gitignored).
#
# Gates:
#   1. go test -race -count=1 ./test/security/...   (security suite, race)
#   2. go test -count=1 ./...                      (no regression)
#   3. go vet ./...
#   4. gitleaks dir .                              (working tree, no history)
#   5. govulncheck ./...                           (reachable vulnerabilities)
#   6. gosec -severity high -confidence high ./... (CI gate)
#   7. gosec -severity medium -confidence medium   (inventory, non-gating)
#
# Requirements: Go >= 1.27.4, gcc (for -race), gitleaks, govulncheck, gosec.
# Optional: staticcheck.
#
# Environment:
#   NO_RACE=1    skip the -race run (no CGO/gcc available)
#   GO_BIN=...   Go binary to use (default: go)
#   SECBIN_DIR   directory with scanner binaries (default: C:\tools\secbin on
#                Windows/Git Bash, otherwise PATH)
#
# Usage: bash scripts/security/scan.sh

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

report_dir="$repo_root/bin/security"
mkdir -p "$report_dir"

go_bin=${GO_BIN:-go}
secbin=${SECBIN_DIR:-}
if [[ -z "$secbin" && -d /c/tools/secbin ]]; then
  secbin=/c/tools/secbin
fi

resolve_tool() {
  local name=$1
  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return 0
  fi
  if [[ -n "$secbin" && -x "$secbin/$name" ]]; then
    printf '%s\n' "$secbin/$name"
    return 0
  fi
  echo "required tool '$name' not found on PATH (set SECBIN_DIR or install it)" >&2
  exit 1
}

gitleaks_bin=$(resolve_tool gitleaks)
govulncheck_bin=$(resolve_tool govulncheck)
gosec_bin=$(resolve_tool gosec)
staticcheck_bin=$(command -v staticcheck || true)

if [[ -z "${CGO_ENABLED:-}" ]]; then
  export CGO_ENABLED=1
fi

gate() {
  local name=$1
  shift
  echo
  echo "==> $name"
  set +e
  "$@"
  local status=$?
  set -e
  if [[ $status -ne 0 ]]; then
    echo "GATE FAILED: $name (exit code $status)" >&2
    exit "$status"
  fi
}

echo "OPF security scan - $repo_root"
echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo
echo "--- tool versions ---"
"$go_bin" version
echo "gitleaks:    $("$gitleaks_bin" version 2>&1 || true)"
echo "govulncheck: $("$govulncheck_bin" -version 2>&1 | head -n 1 || true)"
echo "gosec:       $("$gosec_bin" -version 2>&1 | head -n 1 || true)"
if [[ -n "$staticcheck_bin" ]]; then
  echo "staticcheck: $("$staticcheck_bin" -version 2>&1 || true)"
else
  echo "staticcheck: not installed (optional)"
fi
echo "CGO_ENABLED=$CGO_ENABLED"

if [[ "${NO_RACE:-0}" == "1" ]]; then
  gate "go test ./test/security/..." "$go_bin" test -count=1 ./test/security/...
else
  gate "go test -race ./test/security/..." "$go_bin" test -race -count=1 ./test/security/...
fi
gate "go test ./..." "$go_bin" test -count=1 ./...
gate "go vet ./..." "$go_bin" vet ./...

gate "gitleaks dir ." "$gitleaks_bin" dir . --no-banner --redact \
  --report-format json --report-path "$report_dir/gitleaks.json"

gate "govulncheck ./..." "$govulncheck_bin" ./...

gate "gosec -severity high -confidence high ./..." \
  "$gosec_bin" -severity high -confidence high ./...

echo
echo "==> gosec -severity medium -confidence medium (inventory, non-gating)"
set +e
"$gosec_bin" -severity medium -confidence medium -fmt json \
  -out "$report_dir/gosec-medium.json" ./... >/dev/null 2>&1
medium_status=$?
set -e
if command -v python3 >/dev/null 2>&1 && [[ -f "$report_dir/gosec-medium.json" ]]; then
  count=$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["Issues"]))' "$report_dir/gosec-medium.json" 2>/dev/null || echo '?')
  echo "gosec medium/medium findings: $count (triaged in docs/SECURITY.md)"
else
  echo "gosec medium/medium: report written to $report_dir/gosec-medium.json (exit $medium_status)"
fi

if [[ -n "$staticcheck_bin" ]]; then
  echo
  echo "==> staticcheck ./... (informational)"
  set +e
  "$staticcheck_bin" ./...
  static_status=$?
  set -e
  if [[ $static_status -ne 0 ]]; then
    echo "staticcheck reported findings (exit $static_status); triaged in docs/SECURITY.md"
  fi
fi

echo
echo "ALL SECURITY GATES PASSED"
echo "reports: $report_dir"
