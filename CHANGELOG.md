# Changelog

All notable changes to **Offline Package Fetcher (OPF)** are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.3] - 2026-11-21

Desktop GUI for every supported platform and curated release notes.

### Added

- **Windows GUI** (`opf-gui-windows-amd64.exe.zip`), cross-compiled with
  MinGW-w64 and published as a raw `.exe` inside a `.zip`.
- **macOS GUI** for Intel (`opf-gui-darwin-amd64.tar.gz`) and Apple Silicon
  (`opf-gui-darwin-arm64.tar.gz`), built natively on macOS runners.
- Curated, bilingual release notes for every version under
  `.github/release-notes/`; existing release pages are synced by a dedicated
  workflow and new tags use the curated body.
- Fifteen release assets: CLI/TUI (Linux, Windows, macOS), GUI (Linux, Windows,
  macOS Intel/Apple Silicon) and `SHA256SUMS`.

### Changed

- CI and release workflows build and verify GUI artifacts for Linux, Windows
  and macOS (previously only Linux).
- Windows GUI follows the CLI/TUI Windows policy: raw `.exe` inside `.zip`,
  no UPX (Cortex XDR blocks packed PE); macOS GUI is `.tar.gz`; Linux GUI is
  UPX-packed.

### Notes

- The functional core, CLI and TUI are unchanged from v0.2.2; this release adds
  the missing GUI artifacts and the release-note pages.

## [0.2.2] - 2026-11-21

Packaging correction: 0.2.1 shipped UPX-packed Windows executables, which Cortex XDR blocks.
Raw Go executables run normally, so Windows now ships raw inside `.zip` archives.

### Changed

- Windows CLI/TUI: the `.exe` is **not UPX-packed**; it is published raw inside
  `opf-{cli,tui}-windows-amd64.exe.zip` (~6.1/6.4 MB download instead of ~14.8/15.7 MB). The
  extracted executable runs under Cortex XDR and comparable EDR products.
- Linux CLI/TUI/GUI keep UPX `--best --lzma`; macOS keeps `.tar.gz` archives.
- CI/release now package and verify the Windows zip contents; `make package-windows` reproduces
  the zip locally.
- Code signing with a trusted certificate remains optional but recommended; it is not required
  for Cortex XDR to run the extracted Windows binary.

## [0.2.1] - 2026-11-21

Packaging-only release: the functional Go code is unchanged from 0.2.0; the release assets now use the
strongest compression that the GitHub Actions runners can produce.

### Changed

- Windows CLI/TUI `.exe` files are now **UPX-packed in CI** (`--best --lzma`), dropping them from
  ~14.8/15.7 MB to ~4.8/5.0 MB. Endpoint-protection products may require allow-listing the
  downloaded executable; `make build` still produces an uncompressed build when needed.
- macOS CLI/TUI binaries are published as **`.tar.gz` archives** instead of raw Mach-O files
  (~5.6–6.3 MB instead of 13.8–15.5 MB); extract with `tar -xzf <asset>.tar.gz`.
- Linux assets remain UPX-packed. The release job re-verifies every packed artifact with
  `upx -t`, verifies the macOS archive contents, and continues to publish 12 assets (11 binaries
  plus `SHA256SUMS`).

### Fixed

- The release smoke test restores the executable bit on the downloaded Linux CLI before running
  `-h` (GitHub artifact downloads do not preserve file permissions).

## [0.2.0] - 2026-11-21

Interface language release: the GUI, TUI and CLI now follow the operating system language
(English fallback) and can be switched at runtime, the Fyne translation failure is fixed, and the
release binaries are significantly smaller.

### Added

- **Interface language** for the whole application: OPF follows the operating system UI language
  by default, with **English** as the fallback when the OS language is not English, Brazilian
  Portuguese (pt-BR) or Spanish (es).
- **Global Settings selector** in the GUI (**Idioma da interface**: Automático (sistema),
  English, Português (BR), Español) and in the TUI (**Idioma da interface** text field accepting
  `auto`, `en`, `pt-BR` or `es`); the change applies immediately and is persisted to
  `config.json` (`language`, default `auto`).
- **CLI language control**: `-lang`/`--lang auto|en|pt-BR|es` and the `OPF_LANG` environment
  variable. Precedence is explicit flag > `OPF_LANG` > operating-system detection.
- **English and Spanish catalogs** covering the GUI (297 keys), TUI (186), CLI (113), core
  pipeline messages (102) and Markdown reports (43), plus 8 shared strings. Brazilian Portuguese
  remains the source language.
- **Interface-language security tests**: injected and hostile language values are rejected,
  resolution never leaves the supported set and translated format strings keep user data as
  data.

### Changed

- Flag help/usage, error messages, console output, pipeline stage names and messages, and
  Markdown report labels follow the selected language. JSON keys, status codes, report role
  values, event messages already stored in SQLite and pipeline stage labels already recorded stay
  as they were written.
- `-h`/`--help` now exits with **status 0** (previously 1) and prints the usage text in the active
  language.
- Markdown reports are rendered in the language active at export time; events stored in SQLite
  keep the language they were recorded in.
- Tests are pinned to `OPF_LANG=pt-BR`, so the suite is deterministic regardless of the host
  language.
- **Smaller binaries**: production builds use `-trimpath -ldflags "-s -w -buildid="` and the
  Linux assets are UPX-compressed with `--best --lzma`, cutting their size by 63–72%:
  CLI 14.5 MB → 4.5 MB (-68.72%), TUI 15.4 MB → 4.9 MB (-67.87%) and GUI 33.8 MB → 12.3 MB
  (-63.55%) on linux/amd64; CLI/TUI linux/arm64 shrink by 71.23% and 71.64%. Windows and macOS
  assets stay uncompressed because endpoint-protection products block UPX-packed PE files and
  UPX cannot pack Mach-O. The release publishes 12 assets (11 binaries plus `SHA256SUMS`).

### Fixed

- **Fyne translation failure** in the GUI: the missing Fyne `(Select one)` translations for
  pt-BR and es were added, together with localized placeholders on every `Select` control; the
  GUI now runs in English and Spanish without stderr translation errors.

### Security

- Security suite re-executed for 0.2.0: **56 top-level tests** (33 in `test/security/**` and 23
  white-box tests); gosec high/high reports **0 issues** (1 audited `#nosec`), gitleaks reports
  **0 leaks**, govulncheck reports **0 reachable vulnerabilities** (1 non-reachable advisory,
  GO-2026-5158, in `otel` v1.41.0), actionlint is clean and staticcheck reports 3 informational
  findings. The seven advisories remediated for 0.1.0 remain fixed.

## [0.1.0] - 2026-11-21

Initial public release.

### Added

- Three front ends sharing one pipeline core: **CLI** (`opf-cli`), **TUI**
  (Bubble Tea, `opf-tui`) and **GUI** (Fyne, `opf-gui`).
- Offline download of Linux packages with full recursive dependency resolution
  for **APT** (Ubuntu/Debian), **YUM/DNF** (CentOS, RHEL, Rocky Linux,
  AlmaLinux, Fedora) and **APK** (Alpine).
- Isolated download environments through **Docker** (all families) and
  **WSL** (Windows, APT only).
- 13-stage session pipeline with resumable checkpoints, cancellation and
  guaranteed cleanup.
- Three-point **SHA-256** integrity verification (isolated environment, host
  manifest and remote destination) with a single `SHA256SUMS` manifest per
  session.
- **SSH/SFTP** delivery with strict `known_hosts` verification by default,
  multi-hop jump hosts, key or password authentication and `sudo` installation.
- Explicit install confirmation, optional unattended `--yes` and per-package
  installation status tracking.
- **SQLite** session database (sessions, packages, events, stages and requested
  packages), session archival, and JSON/Markdown report export.
- Optional external **syslog** forwarding (UDP/TCP, RFC 5424).
- Additional package repositories (APT, RPM and APK) with GPG/TLS verification
  enabled by default and explicit insecure opt-ins.
- Remote target detection through `/etc/os-release` and `uname -m`.
- Unit, integration and security test suites.
- GitHub Actions CI that compiles CLI/TUI/GUI, runs tests and security scans,
  plus tag-driven release artifacts.
- Bilingual documentation (English and Brazilian Portuguese), user manual,
  design documents and security report.

### Security

- Strict SSH host key verification by default; disabling it is an explicit,
  documented opt-in.
- Shell-quoted artifact names and validated package, target and repository
  inputs.
- Password material kept in memory/stdin and excluded from persisted session
  configuration.
- Secret and dependency scanning integrated into local scripts and CI.
- Dependency remediation shipped in 0.1.0: `github.com/klauspost/compress`
  v1.20.2, `github.com/moby/go-archive` v0.3.0,
  `go.opentelemetry.io/otel` v1.41.0 and `golang.org/x/image` v0.43.0 (plus
  their transitive updates), closing the reachable advisories GO-2026-6865,
  GO-2026-6253, GO-2026-5506, GO-2026-5066, GO-2026-5062, GO-2026-5032 and
  GO-2026-4815. `govulncheck ./...` reports no reachable vulnerabilities.
