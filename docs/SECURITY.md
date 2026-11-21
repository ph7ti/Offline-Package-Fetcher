# Security Report — Offline Package Fetcher 0.1.0

[English](SECURITY.md) | [Português (BR)](SECURITY.pt-BR.md)

| Field | Value |
|:---|:---|
| Project | Offline Package Fetcher (OPF) |
| Version | **0.1.0** |
| Repository | <https://github.com/ph7ti/Offline-Package-Fetcher> |
| Report date | **2026-11-21** (America/Sao_Paulo) |
| Tests executed on | **2026-11-21** |
| Scope owner | Application security engineer (release team) |
| Status | Executed security suite + static/dynamic scans, findings triaged |

This document records the security work performed for the v0.1.0 public release: the threat
model, the control/test matrix, the tools and commands that were actually executed, the raw
evidence paths, the findings and their remediation status, and the residual risks accepted for
this release. Nothing in this report is theoretical: every control mapped below is backed by an
executed test or scanner run. Commands and trimmed outputs are reproduced in
[§5](#5-execution-results-and-evidence) and can be re-run with `scripts/security/scan.ps1`
(Windows) or `scripts/security/scan.sh` (Linux/macOS).

---

## 1. Scope and threat model

### 1.1 In scope

- Go source in `internal/**`, `cmd/**` and `ui/**` that handles untrusted input, credentials,
  remote command construction, artifact integrity and persistence.
- The release pipeline surfaces that consume untrusted values: package requests, repository
  definitions, SSH/jump-host definitions, package-manager output, artifact names, manifest
  entries and event/report text.
- The security test suite under `test/security/**` plus the narrowly scoped white-box tests
  listed in [§4.3](#43-test-inventory).

### 1.2 Out of scope

- Live exploitation against real SSH servers, real Docker/WSL environments or real package
  repositories. The suite uses fakes, temporary directories and dummy secrets so it is
  deterministic and runs in CI without infrastructure.
- Third-party runtime security (Docker daemon, WSL kernel, OpenSSH server, distribution package
  signing keys), except where OPF chooses a policy for them.
- GUI rendering internals (Fyne/OpenGL) beyond dependency scanning.
- Build/release supply chain beyond the scanners listed here (reproducible builds, signing,
  provenance attestations are tracked separately by the release owner).

### 1.3 Assets

1. SSH credentials (private key path, SSH password, sudo password, jump-host passwords).
2. The remote destination host: OPF executes package installation there through SSH.
3. Downloaded artifacts and their integrity (SHA-256 manifest, three verification points).
4. The local SQLite database, session reports and optional syslog events.
5. The isolated download environment (Docker container or WSL distro).

### 1.4 Trust boundaries and attacker capabilities

| Boundary | Untrusted input | Attacker capability modelled |
|:---|:---|:---|
| Operator → CLI/TUI/GUI | package names, repository URLs, SSH/jump fields, paths | Malicious or careless operator input; fat-fingered/scripted arguments |
| Package manager → OPF | `apt-cache`, `repoquery`, `apk fetch`, `dpkg-query`, `rpm -q`, `apk info` output | Compromised or hostile repository/package metadata trying to inject names or extra lines |
| Remote target → OPF | `/etc/os-release`, `uname`, inventory output, SSH host key | Compromised destination host trying to break parsing or impersonate another host |
| Artifact files → OPF | artifact filenames/content, `SHA256SUMS` entries | Tampered artifact tree, path traversal, duplicate names, symlinks |
| OPF → remote shell | installer scripts, checksum verification, pre-check commands | Shell metacharacters in any interpolated value |
| OPF → local storage | SQLite rows, config JSON, reports, syslog | SQL injection, secret leakage into persisted/exported data |

### 1.5 Security objectives

- **O1** No user/repository/target-controlled value can execute shell commands outside the
  intended remote operation.
- **O2** Secrets never appear in generated commands, persisted session config, reports or logs.
- **O3** Artifact integrity is verified and any tampering is fatal.
- **O4** Host identity is verified strictly by default; insecure modes are explicit opt-ins.
- **O5** Untrusted textual data cannot corrupt the database or the parsing state machine.

---

## 2. Methodology

1. **Threat modelling** of each trust boundary (above), derived from the documented pipeline
   (13 stages) and the public CLI/TUI/GUI surface.
2. **Behavioural tests** against exported APIs (`installer`, `transfer`, `resolver`,
   `inspector`, `store`, `config`, `app`, `observability`). Tests assert observable output:
   generated shell text, SQLite state, returned errors, parsed values and emitted bytes.
3. **Narrowly scoped white-box tests** where the security control lives in an unexported
   function and no exported API exposes it: `shellQuote`, `wslShellQuote`/`wslShellJoin`,
   `wrapSudoCommand`, `buildSSHClientConfig`, `validateRemoteDir`, `configJSON`, parser
   internals and syslog framing.
4. **Static analysis**: gosec (CI gate + full inventory), govulncheck (reachable
   vulnerabilities), gitleaks (working-tree secrets), staticcheck (informational), `go vet`.
5. **Cross-platform execution**: the suite was executed on Windows (primary development
   platform, including `-race`) and inside a Linux `golang:1.27.4` container to catch
   platform-specific assumptions in path/permission/symlink tests.
6. **Honest reporting**: every scanner result below is copied from an actual run. Findings that
   are intentional opt-ins are annotated in code (`#nosec`, `gitleaks:allow`) and documented
   here; nothing was silenced without a written rationale.

Limitations are listed in [§8](#8-limitations).

---

## 3. Control / test matrix

Legend: **PASS** = the test asserts the control and passed on 2026-11-21 on Windows and Linux
(where the test is cross-platform). Test names are exactly as reported by `go test -v`.

### 3.1 Command injection and shell quoting

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C1.1 | `apt`/`rpm`/`apk` installer quotes every artifact filename with POSIX single quotes; injection payloads (`;`, `|`, `&`, `$()`, backticks, quotes, spaces, tabs) never appear outside quotes | `TestInstallerCommandInjection` (apt/rpm/apk) | PASS |
| C1.2 | Remote session directory is shell-quoted in `cd '...'` scripts | `TestInstallerRemoteDirInjection` | PASS |
| C1.3 | Names that cannot be safely handled (NUL, CR/LF, wrong suffix, duplicates, empty path) are rejected before a command is built | `TestInstallerRejectsUnsafeArtifactNames` | PASS |
| C1.4 | Remote directory must be absolute and free of NUL/CR/LF | `TestSecurityValidateRemoteDir` (installer) | PASS |
| C1.5 | `shellQuote` closes/escapes/reopens single quotes correctly | `TestSecurityShellQuote` (installer and transfer), `TestSecurityShellQuoteNeutralizes` | PASS |
| C1.6 | WSL command joining quotes every argument | `TestSecurityWSLShellQuote`, `TestSecurityWSLShellJoin` (Windows build tag) | PASS |
| C1.7 | sudo password never appears in the command; `sudo -S -p '' sh -c` reads it from stdin | `TestSudoPasswordNeverInCommand`, `TestSudoPasswordGoesThroughStdin` (apt/rpm/apk) | PASS |
| C1.8 | Password with CR/LF/NUL is rejected (cannot inject into sudo stdin) | `TestSudoPasswordRejectsControlCharacters`, `TestSecurityWrapSudoCommand` | PASS |

### 3.2 Input validation

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C2.1 | Resolvers reject hostile package names (`vim;id`, `$(id)`, backticks, spaces, newlines, `../etc/passwd`, `--help`, NUL) **before any executor call** | `TestResolversRejectInjectedPackageNames` (apt/rpm/apk) | PASS |
| C2.2 | Architecture and isolated work directory are validated before execution | `TestResolversRejectInjectedArchitectureAndWorkDir` | PASS |
| C2.3 | Positive control: legitimate names still resolve | `TestResolversAcceptValidNames` | PASS |
| C2.4 | Repository URLs reject credentials in URL, non-HTTP(S) schemes, control characters, fragments, missing host, `$basearch` on APT, conflicting TLS/GPG opt-ins, duplicate slugs, suite/component injection | `TestRepositoryURLValidation`, `TestDuplicateRepositorySlugRejected` | PASS |
| C2.5 | Detected target architecture rejects injection values; unknown distro rejected | `TestTargetArchValidation` | PASS |
| C2.6 | Jump hosts require host/user/auth mode, numeric port in range; errors never include the password | `TestSSHJumpInputValidation` | PASS |
| C2.7 | Jump/destination hosts with CR/LF/NUL are rejected before any TCP dial; jumps without credentials are rejected | `TestSSHJumpControlCharactersRejectedBeforeDial`, `TestSSHJumpRequiresCredentials`, `TestSecuritySSHConfigRejectsControlChars` | PASS |
| C2.8 | Remote pre-check validates names before building a command; malformed inventory lines are dropped | `TestInspectorRejectsInjectedPackageNames`, `TestInspectorCommandIsQuotedAndOutputIsParsed`, `TestSecurityParseInstalledRejectsMalformedLines` | PASS |
| C2.9 | Syslog tag/protocol whitelist blocks header injection | `TestSyslogConfigInjection`, `TestSecuritySyslogTagPattern` | PASS |

### 3.3 SQL injection

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C3.1 | SQL payloads in package names, versions, session fields, event text/details and `pacotes_solicitados` are stored literally; schema/tables remain intact | `TestSQLInjectionStore` | PASS |
| C3.2 | Dynamic `SET` clauses (verification metadata) bind values as parameters; payloads stored literally | `TestSQLInjectionStore` (same test, update phase) | PASS |
| C3.3 | Invalid enum status is rejected before SQL executes | `TestStoreRejectsInvalidStatusDespiteInjection` | PASS |

### 3.4 Path traversal and symlink abuse

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C4.1 | Manifest entries with `../`, absolute paths, nested paths, duplicates or extra fields are rejected | `TestManifestRejectsTraversalAndMalformedEntries` | PASS |
| C4.2 | Manifest verification rejects symlinks to non-regular files | `TestManifestRejectsSymlinkTraversal` (skips only where the OS forbids symlinks) | PASS |
| C4.3 | Manifest entries use `filepath.Base` only; duplicate basenames are rejected | `TestManifestEntriesUseBasename` | PASS |
| C4.4 | Remote paths reject NUL/CR/LF | `TestSecurityValidateRemotePath` (transfer) | PASS |
| C4.5 | Non-regular/empty/missing artifacts are rejected before a manifest is published | `TestGerarManifestoRejectsUnsafeArtifacts` | PASS |

### 3.5 Integrity

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C5.1 | Any content change after manifest generation is detected (same-size rewrite and truncation) | `TestManifestTamperDetected` | PASS |
| C5.2 | Empty/malformed manifests and digest mismatches are fatal | `TestManifestRejectsTraversalAndMalformedEntries`, `TestManifestTamperDetected` | PASS |
| C5.3 | Digest comparison uses `crypto/subtle.ConstantTimeCompare` (source guard + behavioural mismatch test) | `TestManifestComparisonUsesConstantTime`, `TestManifestTamperDetected` | PASS |

### 3.6 Secret handling

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C6.1 | `sessions.config_json` (persisted and exported) contains no SSH/jump/sudo password and no key material; jump metadata keeps only non-secret auth mode | `TestSecurityConfigJSONExcludesSecrets`, `TestSecurityNewSessionConfigJSONExcludesSecrets` | PASS |
| C6.2 | Pipeline/event code has no credential surface (regression guard) | `TestSecurityPipelineHasNoPasswordSurface` | PASS |
| C6.3 | Global settings have no secret fields; saved file has no secret markers and is `0600` on POSIX | `TestSettingsNeverPersistSecrets`, `TestConfigLoadAcceptsLoosePermissions` | PASS |
| C6.4 | Jump metadata/fingerprint exclude the password; in-memory hop still authenticates | `TestSSHJumpMetadataAndFingerprintExcludePasswords`, `TestSSHJumpBuildKeepsSecretsInMemoryOnly` | PASS |
| C6.5 | sudo password travels only through stdin | `TestSudoPasswordGoesThroughStdin`, `TestSudoPasswordNeverInCommand` | PASS |

### 3.7 SSH host key policy

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C7.1 | Strict by default: empty `known_hosts` rejects unknown keys; matching entry accepted; hostname/port mismatch and MITM key rejected; missing `known_hosts` file is an error | `TestSecurityHostKeyPolicyStrictByDefault` | PASS |
| C7.2 | Only `InsecureSkipHostKey=true` disables verification (explicit opt-in) | `TestSecurityInsecureSkipHostKeyIsExplicitOnly` | PASS |
| C7.3 | Host validation rejects control characters | `TestSecuritySSHConfigRejectsControlChars` | PASS |

### 3.8 Parsing of dependency/monitoring output

| # | Control | Test(s) | Result |
|:---|:---|:---|:---|
| C8.1 | Unexpected `apt-cache` lines abort resolution; injection lines rejected | `TestSecurityAptCacheOutputRejection` | PASS |
| C8.2 | `repoquery` output filtered to the package-name whitelist; empty result is fatal | `TestSecurityRepoqueryOutputRejection` | PASS |
| C8.3 | `apk fetch --simulate` output cannot smuggle traversal/shell payloads | `TestSecurityApkSimulateOutputRejection` | PASS |
| C8.4 | Inventory lines with wrong field counts / non-installed states are dropped; values are treated as data (no command built from them) | `TestSecurityParseInstalledRejectsMalformedLines`, `TestSecurityParseInstalledTreatsNamesAsData`, `TestSecuritySplitAPKVersionStructuralValidation` | PASS |
| C8.5 | Syslog messages with newlines/CR/NUL/ANSI are JSON-escaped onto a single RFC 5424 line and round-trip unchanged | `TestSecuritySyslogFormatSanitizesMessage` | PASS |

### 3.9 Tooling controls

| # | Control | Evidence | Result |
|:---|:---|:---|:---|
| C9.1 | Security suite races are clean | `go test -race -count=1 ./test/security/...` | PASS |
| C9.2 | Full regression suite | `go test -count=1 ./...` (all packages) | PASS |
| C9.3 | High-severity/high-confidence static analysis gate | `gosec -severity high -confidence high ./...` → 0 issues | PASS |
| C9.4 | No secrets in the working tree | `gitleaks dir . --no-banner --redact` → no leaks | PASS |
| C9.5 | No reachable known vulnerabilities | `govulncheck -show verbose ./...` → 0 reachable | PASS |
| C9.6 | Vet/format/CI lint | `go vet ./...`, `gofmt -l`, `actionlint` → clean | PASS |

---

## 4. Tools, versions and test inventory

### 4.1 Tool versions (verified 2026-11-21)

| Tool | Version | Notes |
|:---|:---|:---|
| Go | `go1.27.4 windows/amd64` | Primary run; Linux run in container |
| gcc / CGO | MinGW-W64 UCRT `gcc.exe 16.2.0` | Required for `-race` on Windows |
| Docker | `29.9.2` (client) | Used to run `golang:1.27.4 linux/amd64` |
| gitleaks | `8.30.1` (release binary) | A build-from-source binary was also used by the Lead; it prints `version is set by build process` and produced the same result (0 leaks) |
| govulncheck | `v1.10.0`, DB `https://vuln.go.dev` updated `2026-11-11 02:51:11 UTC` | `govulncheck -version` |
| gosec | module `github.com/securego/gosec/v2 v2.30.0` | `gosec -version` prints `Version: dev` (source build); module version read with `go version -m` |
| staticcheck | `2026.2.2 (0.8.2)` | Informational |
| actionlint | `v1.7.12` | Workflow lint |
| go vet | bundled with Go 1.27.4 | — |

### 4.2 Commands executed (exact)

```text
go test -race -count=1 ./test/security/...
go test -race -count=1 -run 'TestSecurity' ./internal/...
go test -count=1 ./...
go vet ./...
gofmt -l .
gitleaks dir . --no-banner --redact
govulncheck -show verbose ./...
gosec -severity high -confidence high ./...
gosec -severity medium -confidence medium -fmt json -out bin/security/gosec-medium.json ./...
staticcheck ./...
actionlint
```

Docker (Linux semantics):

```text
docker run --rm -v "<repo>:/src" -w /src -e CGO_ENABLED=1 \
  -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache golang:1.27.4 \
  sh -c "go test -count=1 ./test/security/... ./internal/... && \
         go test -race -count=1 ./test/security/..."
```

### 4.3 Test inventory

| Location | Top-level tests | Runs incl. subtests | Content |
|:---|:---:|:---:|:---|
| `test/security/**` | 30 | 88 | Threat-model suite (this document) |
| `internal/app/security_test.go` | 3 | 3 | `configJSON` secret exclusion, pipeline guard |
| `internal/transfer/security_test.go` | 5 | 5 | Host-key strictness, quoting, remote path |
| `internal/installer/security_test.go` | 4 | 4 | Quoting, sudo wrapper, remote dir |
| `internal/observability/security_test.go` | 2 | 2 | Syslog framing/tag injection |
| `internal/resolver/security_test.go` | 4 | 4 | apt-cache/repoquery/apk parsing |
| `internal/inspector/security_test.go` | 3 | 3 | Inventory parsing |
| `internal/fetcher/security_windows_test.go` | 2 | 2 | `wslShellQuote`/`wslShellJoin` (Windows only) |
| **Total** | **53** | **111** | Executed 2026-11-21 |

### 4.4 Platform-conditional tests

| Test | Condition | Why it is safe |
|:---|:---|:---|
| `TestSecurityWSLShellQuote`, `TestSecurityWSLShellJoin` | `//go:build windows` | They exercise `internal/fetcher/wsl_windows.go`, which only compiles on Windows. |
| `TestManifestRejectsSymlinkTraversal` | `t.Skip` if `os.Symlink` is unavailable | Windows without symlink privilege cannot create the fixture; on Linux the test runs. |
| `TestSettingsNeverPersistSecrets` (mode assertion) | POSIX only (`runtime.GOOS != "windows"`) | Windows has no POSIX permission bits; content checks still run everywhere. |
| `TestConfigLoadAcceptsLoosePermissions` | `chmod` is a no-op on Windows | Asserts content round-trip; permission tightening is asserted by the POSIX branch above. |

All other tests are platform-neutral. The Linux container run on the final dependency tree
passed `./test/security/...`, `./internal/...` and `-race ./test/security/...`, including the
symlink test that skips on unprivileged Windows.

---

## 5. Execution results and evidence

Raw artifacts are written to `bin/security/` (gitignored) by `scripts/security/scan.*`.

### 5.1 Test suites

```text
$ go test -race -count=1 ./test/security/...
ok  	opf/test/security	9.794s

$ go test -count=1 ./...
ok  	opf/cmd/opf-cli	1.908s
?   	opf/cmd/opf-gui	[no test files]
?   	opf/cmd/opf-tui	[no test files]
ok  	opf/internal/app	2.220s
ok  	opf/internal/config	1.101s
ok  	opf/internal/core	5.087s
ok  	opf/internal/fetcher	1.920s
ok  	opf/internal/inspector	1.651s
ok  	opf/internal/installer	0.813s
ok  	opf/internal/observability	3.146s
ok  	opf/internal/resolver	0.848s
ok  	opf/internal/store	2.461s
ok  	opf/internal/transfer	0.942s
ok  	opf/test/security	2.240s
ok  	opf/ui/gui	10.900s
ok  	opf/ui/tui	1.517s
```

Linux (final tree, `golang:1.27.4 linux/amd64`): `ok` for `test/security`, all `internal/*`
packages and `-race ./test/security/...` (exit 0). This validates the platform-conditional
tests under POSIX semantics.

### 5.2 gitleaks

First run against the working tree reported one finding, which was triaged as a **false
positive**:

```text
File: internal/app/sshjumps_test.go:179
Rule: generic-api-key
Match: Password: "REDACTED"      (dummy fixture value "jump2-segredo")
```

Evidence: the value is a hard-coded dummy used by an existing unit test to prove passwords are
not persisted; it is not a live credential, and the file contains only test fixtures. The Lead
fixed the source with an inline annotation on that line:

```go
{Host: "jump2", ..., Password: "jump2-segredo"}, // gitleaks:allow -- dummy fixture password
```

Final verification:

```text
$ gitleaks dir . --no-banner --redact
INF scanned ~1835022 bytes (1.84 MB) in 377ms
INF no leaks found
exit 0
```

No allowlist file is used; the scan is the bare working-tree command, matching what CI's
gitleaks action does.

### 5.3 govulncheck

**Pre-remediation evidence** (before the dependency upgrades, current tree at the time):

```text
Your code is affected by 7 vulnerabilities from 4 modules.
```

| Advisory | Module (found → fixed) | Reachability (pre-fix) |
|:---|:---|:---|
| GO-2026-6865 | `github.com/klauspost/compress` v1.18.0 → v1.20.2 | reachable (compression init / archive paths) |
| GO-2026-6253 | `github.com/moby/go-archive` v0.1.0 → v0.3.0 | reachable (Docker copy/archive) |
| GO-2026-5506 | `go.opentelemetry.io/otel` v1.38.0 → v1.41.0 | reachable (OTel baggage handling) |
| GO-2026-5066 | `golang.org/x/image` v0.24.0 → v0.43.0 | reachable via `ui/gui/ui.go:102` (`SetIcon` → `tiff.Decode`) |
| GO-2026-5062 | `golang.org/x/image` v0.24.0 → v0.43.0 | same GUI icon path |
| GO-2026-5032 | `golang.org/x/image` v0.24.0 → v0.41.0 | same GUI icon path |
| GO-2026-4815 | `golang.org/x/image` v0.24.0 → v0.38.0 | same GUI icon path (plus a reported `checksum.go:41` trace) |

**Final state** (dependencies upgraded by the Lead in the release tree, re-verified by this
task):

```text
go.mod: github.com/klauspost/compress v1.20.2, github.com/moby/go-archive v0.3.0,
        go.opentelemetry.io/otel v1.41.0, golang.org/x/image v0.43.0

$ govulncheck -show verbose ./...
=== Symbol Results ===
No vulnerabilities found.
...
Your code is affected by 0 vulnerabilities.
This scan also found 1 vulnerability in packages you import and 1 vulnerability
in modules you require, but your code doesn't appear to call these vulnerabilities.
exit 0
```

Non-reachable items reported by the same scan (documented as residual, no code path):

| Advisory | Module | Status |
|:---|:---|:---|
| GO-2026-5158 (baggage header length cap) | `go.opentelemetry.io/otel` v1.41.0, fixed in v1.42.0 | Package-level only; not called by OPF (`Symbol Results: none`). Candidate for a future upgrade. |
| GO-2026-6222 (VP8L decoding memory) | `golang.org/x/image` v0.43.0, fixed in v0.45.0 | Module-level only; decoder not reached by OPF paths (`Symbol Results: none`). Candidate for a future upgrade. |

All seven advisories that were reachable pre-release are therefore **remediated in v0.1.0**,
and the two remaining advisories are **non-reachable** per govulncheck's symbol analysis.
`go test ./...` passed after each upgrade step and on the final tree.

### 5.4 gosec

High-severity/high-confidence gate (same command as `.github/workflows/ci.yml`):

```text
$ gosec -severity high -confidence high ./...
Summary:
  Gosec  : dev
  Files  : 45
  Lines  : 13643
  Nosec  : 1
  Issues : 0
exit 0
```

The single `#nosec` is the auditable annotation for the intentional repository TLS opt-in:

```go
// internal/fetcher/repositories.go:359
// #nosec G402 -- explicit, documented opt-in (--repo-insecure-tls); see docs/SECURITY.md
tlsConfig.InsecureSkipVerify = true
```

Full inventory at medium severity/confidence: **21 findings**, all triaged in §6. gosec exits
1 for this inventory run by design; it is not a release gate.

### 5.5 staticcheck, go vet, actionlint

```text
$ go vet ./...          exit 0
$ gofmt -l .            (no output)
$ actionlint            exit 0

$ staticcheck ./...
internal/core/pipeline.go:324:2: this value of manifestPath is never used (SA4006)
internal/core/pipeline_test.go:275:2: field progress is unused (U1000)
internal/resolver/apk.go:121:3: should replace this if statement with an unconditional strings.TrimSuffix (S1017)
ui/gui/components.go:379:14: func (*UI).stepItem is unused (U1000)
ui/gui/ui.go:341:14: func (*UI).setBusy is unused (U1000)
ui/gui/ui.go:369:14: func (*UI).currentPassword is unused (U1000)
ui/gui/ui.go:388:14: func (*UI).statusText is unused (U1000)
```

None of these are security defects: they are dead code, a style suggestion, and a redundant
assignment whose value is overwritten in both branches. They are reported for transparency and
left untouched to avoid out-of-scope refactoring.

---

## 6. Findings

No exploitable vulnerability was found in OPF-owned code. The findings below are the scanner
results and their disposition.

| ID | Severity | Area | Evidence | Status |
|:---|:---|:---|:---|:---|
| SEC-01 | Medium (scanner: HIGH/HIGH) | TLS verification opt-in (`--repo-insecure-tls`) | gosec G402 at `internal/fetcher/repositories.go:359` | **Accepted, annotated.** Intentional per-repository opt-in, logged as a warning, requires a CA or explicit flag; documented in §7. `#nosec G402` with rationale; high/high gate exits 0. |
| SEC-02 | Info | Secret scanning | gitleaks `generic-api-key` in `internal/app/sshjumps_test.go:179` | **Fixed (false positive).** Dummy fixture password; triaged and annotated `// gitleaks:allow`. Final scan: 0 leaks. |
| SEC-03 | Critical→none | Dependencies | GO-2026-6865 `klauspost/compress` v1.18.0 | **Remediated**: v1.20.2; govulncheck 0 reachable. |
| SEC-04 | High→none | Dependencies | GO-2026-6253 `moby/go-archive` v0.1.0 | **Remediated**: v0.3.0; govulncheck 0 reachable. |
| SEC-05 | High→none | Dependencies | GO-2026-5506 `otel` v1.38.0 | **Remediated**: v1.41.0; govulncheck 0 reachable. |
| SEC-06 | High→none | Dependencies | GO-2026-5066 `x/image` | **Remediated**: v0.43.0; govulncheck 0 reachable. |
| SEC-07 | High→none | Dependencies | GO-2026-5062 `x/image` | **Remediated**: v0.43.0; govulncheck 0 reachable. |
| SEC-08 | Medium→none | Dependencies | GO-2026-5032 `x/image` | **Remediated**: v0.43.0; govulncheck 0 reachable. |
| SEC-09 | High→none | Dependencies | GO-2026-4815 `x/image` | **Remediated**: v0.43.0; govulncheck 0 reachable. |
| SEC-10 | Low | Dependencies (non-reachable) | GO-2026-5158 (`otel` 1.41.0 → 1.42.0), GO-2026-6222 (`x/image` 0.43.0 → 0.45.0) | **Accepted residual**: package/module present but no symbol reachable; upgrade in a future release. |
| SEC-11 | Medium (scanner) | SSH host-key bypass (`InsecureIgnoreHostKey`) | G106 `internal/transfer/ssh.go:379` | **Accepted**: only reachable through `--ssh-insecure-skip-host-key`/`InsecureSkipHostKey=true`; strict `known_hosts` is the default and is covered by C7.1. |
| SEC-12 | Medium (scanner) | Subprocess with variable args | G204 `internal/fetcher/wsl_windows.go:364,390` | **Accepted**: `exec.CommandContext("wsl.exe", args...)` with no shell; arguments are validated/quoted by `wslShellJoin`; covered by C1.6. |
| SEC-13 | Medium (scanner) | SQL string concatenation | G202 `internal/store/sqlite.go:810` | **Accepted**: dynamic `SET` clause built only from fixed internal column names; all values bound as parameters; covered by C3.2. |
| SEC-14 | Medium (scanner) | File paths from variables | G304 ×10 (`transfer`, `checksum`, `fetcher`, `core`, `config`, `cmd`) | **Accepted**: paths originate from operator config/session state, not from remote output; traversal-relevant names are validated (C4.x). |
| SEC-15 | Medium (scanner) | File/dir permissions | G301 ×3 (`0755` dirs), G302 ×1 (`0644` manifest) | **Accepted residual**: local artifacts/database may be readable by other local users on multi-user hosts; see §7. |
| SEC-16 | High/Medium confidence | Integer conversion | G115 ×3 `ui/gui/theme.go:210-212` | **Accepted**: color-component math on internal palette values; no untrusted numeric input reaches these conversions. |

### 6.1 Additional observations (not security findings)

| ID | Observation | Impact | Recommendation |
|:---|:---|:---|:---|
| OBS-01 | Verified during Lead review as a **false observation**: in `^[A-Za-z0-9][A-Za-z0-9+._:-]*$` the `-` is the last character inside the character class, so it is a literal hyphen. `ca-certificates`, `py3-pip` and `gcc-c++` all match (confirmed with Go's `regexp` and .NET regex). The remote pre-check does not reject them. | None — no functional defect. | None; no code change required. |
| OBS-02 | staticcheck dead-code/style findings (§5.5). | None. | Optional cleanup. |

---

## 7. Residual and accepted risks

The following risks are accepted for v0.1.0. Each is explicit, documented and, where possible,
mitigated by a safer alternative that the operator can choose.

| ID | Risk | Why it is accepted | Mitigation available |
|:---|:---|:---|:---|
| R1 | `--ssh-password` / `--ssh-jump-password` place secrets in process argv, visible to local process listings and shell history. | CLI compatibility for non-interactive use; changing it now would break existing automation. | Prefer SSH keys; use `OPF_SSH_PASSWORD` / `OPF_SSH_JUMP_PASSWORDS` environment variables, which are supported and preferred by the CLI help text. |
| R2 | `--repo-insecure-tls`, `--repo-insecure-gpg`, `--ssh-insecure-skip-host-key` weaken transport or trust. | Needed for isolated/lab repositories and self-signed hosts; blocking them entirely would remove supported scenarios. | All are explicit opt-ins, logged as warnings, mutually exclusive with the safe alternative (CA/key/host key) where applicable. SEC-01 annotation records the TLS decision. |
| R3 | SQLite database, output directory and `SHA256SUMS` use `0644`/`0755` permissions. | The tool is typically run by a single operator; enforcing `0600`/`0700` could break shared output directories. | Run OPF in a dedicated account or directory with restrictive umask; hardening is a candidate for v0.2.0. |
| R4 | HTTP repository URLs are allowed (GPG signature still mandatory by default). | Air-gapped mirrors are frequently HTTP-only. | Use HTTPS where possible; keep `--repo-insecure-gpg` off; CA/TLS opt-ins are validated against HTTP URLs. |
| R5 | Package authenticity relies on the isolated environment's package manager trust (repository GPG keys) plus SHA-256 integrity of the downloaded set. SHA-256 proves integrity, not publisher authenticity. | This matches the distro trust model OPF operates in; independent signature verification is out of scope for v0.1.0. | Keep GPG verification enabled (default), use trusted mirrors, verify `SHA256SUMS` out-of-band if the host storing artifacts is untrusted. |
| R6 | Inventory metadata from the authenticated target (names/versions) is stored as opaque data; package names are not re-validated against a whitelist. | The target is authenticated and the values are never interpolated into commands; they are only persisted/reported. | Do not use a compromised destination as a source of truth for reports; validation could be tightened in a future release. |
| R7 | Remote install command runs package managers as root (or via sudo) inside the target. | Installing packages requires privilege; OPF requires explicit confirmation before step 10/11. | Review the generated command (logged/streamed), keep `--sudo` scoped, use a dedicated maintenance account. |
| R8 | Docker/WSL isolation depends on the host runtime and runs commands as root inside the isolated environment. | The isolated environment is discarded per session and never receives SSH credentials. | Keep Docker/WSL patched; do not mount host secrets into the isolated environment. |
| R9 | A symlink inside the artifact directory can be verified if its target content matches the manifest hash. | Verification is content-based; an attacker who can already write the artifact tree and knows the hashes can replace content, but cannot escape the directory through manifest names. | Keep artifact directories writable only by the operator; symlinks to non-regular files are rejected (C4.2). |
| R10 | GUI dependency advisories (SEC-10) are not reachable but the modules remain in the binary. | Upgrading further is a release-owner decision; govulncheck symbol analysis shows no call path. | Upgrade `otel` to 1.42.0 and `x/image` to 0.45.0 in a future release and re-run `govulncheck`. |
| R11 | The security suite does not perform live end-to-end attacks against real SSH/Docker/WSL targets. | Deterministic, infrastructure-free CI; the functional integration suite (F0–F5) covers live flows separately. | Run `go test -tags=integration ./...` in a controlled lab before high-assurance deployments. |
| R12 | `-race` coverage is Windows amd64 + Linux amd64; macOS is not exercised in this run. | The release targets Linux/Windows primarily; macOS is secondary. | Add macOS to CI matrix if macOS becomes a supported target. |

---

## 8. Limitations

- Scanners are point-in-time: gitleaks scans the working tree (no git history), govulncheck uses
  the vulnerability DB snapshot from 2026-11-11, and gosec/staticcheck analyse the current
  source. Re-run `scripts/security/scan.*` after dependency or code changes.
- The injection tests assert quoting structurally (single-quoted token, payload only inside
  quotes) rather than executing a real POSIX shell on Windows; the same tests run on Linux in
  the container, where the POSIX semantics are native.
- No fuzzing (`go test -fuzz`) was performed for v0.1.0; parsers are covered by table tests for
  hostile inputs.
- No external penetration test or manual code review by a third party was performed.
- Secret scanning covers the working tree only; git history scanning is performed by CI's
  gitleaks action (full history).

---

## 9. How to reproduce

### 9.1 Local (Windows)

```powershell
$env:GOROOT = 'C:\tools\go1.27.4'
$env:PATH   = 'C:\tools\go1.27.4\bin;' + $env:PATH
$env:CGO_ENABLED = '1'   # needed for -race (gcc/MinGW required)

go test -race -count=1 ./test/security/...
go test -count=1 ./...
pwsh -File scripts/security/scan.ps1        # full gates + reports in bin/security
```

### 9.2 Local (Linux/macOS)

```bash
go test -race -count=1 ./test/security/...
go test -count=1 ./...
./scripts/security/scan.sh                  # full gates + reports in bin/security
```

`scan.sh`/`scan.ps1` are fail-fast, print tool versions, write JSON/text evidence to
`bin/security/` and stop at the first failing gate. `-NoRace` / `NO_RACE=1` skips the race run
where CGO is unavailable.

### 9.3 CI

The release CI already calls the suite (`.github/workflows/ci.yml`, security job):

```yaml
- name: Security test suite
  run: go test -count=1 ./test/security/...
```

and gates on `gosec -severity high -confidence high ./...` plus gitleaks. The full command set
is:

```text
go vet ./...
gofmt -l .            # must be empty
go test -count=1 ./...
go test -count=1 ./test/security/...
gitleaks (action)
gosec -severity high -confidence high ./...
govulncheck ./...     # informational in v0.1.0 (continue-on-error)
```

To add the race variant, use `go test -race -count=1 ./test/security/...` on a Linux runner
with gcc installed.

---

## 10. Disclosure and contact policy

- Please report suspected vulnerabilities **privately** through GitHub Security Advisories:
  <https://github.com/ph7ti/Offline-Package-Fetcher/security/advisories/new>.
- Do not open a public issue with exploit details before a fix is available.
- The maintainers aim to acknowledge within **7 days** and to coordinate disclosure within
  **90 days**, or sooner once a fix is released. Credit is given to reporters who request it.
- Include the OPF version (`opf-cli --version`), platform, exact command, and a minimal
  reproduction using dummy secrets. Never send real credentials.
- This report itself contains no live secrets; every credential-like value is a dummy fixture.

---

## 11. Release statement

- The security test suite defined in this document was **created and executed on 2026-11-21**
  for **Offline Package Fetcher v0.1.0**.
- `go test -race -count=1 ./test/security/...`, the in-package security tests, and
  `go test ./...` all pass on the release tree (Windows) and in a Linux `golang:1.27.4`
  container.
- gosec high/high passes with 0 issues (1 documented `#nosec`), gitleaks reports no leaks,
  govulncheck reports 0 reachable vulnerabilities, and the seven reachable advisories found
  before release are remediated by the dependency upgrades recorded in §5.3.
- The remaining scanner items and the residual risks in §7 are accepted for v0.1.0 and should be
  revisited for v0.2.0.

**Signed-off by:** application security engineer, OPF release team, 2026-11-21.
