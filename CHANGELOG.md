# Changelog

All notable changes to **Offline Package Fetcher (OPF)** are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0] - 2026-11-23

Air-gapped offline bundles, corporate SSH ergonomics, proxy & custom CA support, Podman / rootless Docker, package version pinning, ephemeral private repository authentication and saved session profiles.

### Added

- **Air-Gapped Offline Bundle (`--offline-bundle`)**: packages verified session artifacts, `SHA256SUMS`, `metadata.json` and an executable `install-offline.sh` into a self-contained `.tar.gz`. The script runs P3 `sha256sum -c SHA256SUMS`, supports `--verify-only` and invokes the exact offline install command for APT, YUM/DNF or APK. Available in CLI, GUI and TUI.
- **Corporate SSH Ergonomics**:
  - Passphrase-protected private keys (`ssh.ParsePrivateKeyWithPassphrase`, `--ssh-key-passphrase` / `OPF_SSH_KEY_PASSPHRASE` and per-jump `--ssh-jump-passphrase` / `OPF_SSH_JUMP_PASSPHRASES`), kept strictly in memory.
  - Native `ssh-agent` authentication (`--ssh-agent`, GUI/TUI toggles, and `agent` jump-host auth mode) supporting both Unix `SSH_AUTH_SOCK` and Windows OpenSSH named pipe (`\\.\pipe\openssh-ssh-agent`).
  - `~/.ssh/config` import (`--ssh-config <alias>`, `--ssh-config-file`, and GUI import action) resolving `HostName`, `User`, `Port`, `IdentityFile`, `UserKnownHostsFile`, `Include` and recursive `ProxyJump` chains with cycle protection.
- **Corporate Proxy & Custom CA in Isolated Containers**: `--http-proxy`, `--https-proxy`, `--no-proxy`, `--inherit-proxy`, `--ca-cert` (and Global Settings in GUI/TUI) inject proxy environment variables and install custom PEM CAs into the container OS trust store (Debian/Ubuntu, RHEL/CentOS/Rocky/Alma/Fedora and Alpine) as well as host `KeyURL` downloads. Proxy URLs with embedded credentials are rejected for persistence and redacted from logs/session metadata.
- **Podman & Rootless Docker Support**: `--fetch-method podman`, `--container-host`, and automatic rootless Docker / Podman socket discovery (`CONTAINER_HOST`, `DOCKER_HOST`, `XDG_RUNTIME_DIR/docker.sock`, `XDG_RUNTIME_DIR/podman/podman.sock`, macOS Podman machine and Windows named pipe). Includes automatic SQLite `sessoes.metodo_fetch` schema migration preserving existing sessions.
- **Package Version Pinning**: accepts explicit version specs (`pkg=version` and `pkg:arch=version` for APT, `pkg=version` for APK, `pkg=version` / `pkg-version` for RPM) with strict injection validation across resolvers, fetchers, remote inspector, pre-check and *Package diff*.
- **Ephemeral Private Repository Authentication**: supports `Basic Auth` and `Bearer Token` per additional repository (CLI `--repo-auth-type`, `--repo-auth-user`, `OPF_REPO_PASSWORD`, `OPF_REPO_TOKEN`, `OPF_REPO_AUTH`, plus GUI and TUI editors). Credentials are never serialized (`json:"-"`), written only inside the ephemeral container with `0600` permissions (APT `auth.conf.d` / `ExtraHeader::`, RPM `username`/`password` & `set_http_headers`, APK Basic Auth; Bearer rejected on APK), used for `KeyURL` downloads and redacted from error messages.
- **Saved Session Profiles (zero secrets)**: `profiles.json` (`0600`) CRUD with CLI flags (`--profile`, `--save-profile`, `--list-profiles`, `--delete-profile`, `--profiles-file`) and GUI profile manager. All passwords, passphrases and private repository credentials are excluded and stripped before writing to disk.

## [0.3.1] - 2026-11-23

Offline installation hardening, performance improvements and CLI unification.

### Added

- **CLI** flags `--version` (prints version and build metadata), `--pkg-file <path>` (loads package lists from a text file with `#` comments and comma/newline separators) and `--resume <session-id>` (resumes an existing session directly from SQLite).
- Live output streaming during remote installation even when using `sudo -S` (`RunCommandStreamWithInput`).
- `go test -race` verification step on Linux in CI (`.github/workflows/ci.yml`).

### Performance

- **Package diff → Proceed** now keeps the isolated environment (Docker/WSL) and SSH connection warm across preview and execution, eliminating duplicate container startup and SSH handshakes (with a safety TTL and guaranteed cleanup on abort, error or close).
- `DockerFetcher` copies all verified session artifacts from the isolated container in a single validated tar stream (`CopyArchiveFromContainer`) with tar-slip/symlink protection and per-file size limits.
- `SSHTransferManager.Upload` transfers files concurrently over SFTP using a bounded worker pool (up to 4 workers) with thread-safe progress tracking.
- `AptResolver` runs `apt-get update` once per isolated environment instead of once per root package.

### Fixed

- Remote installers now pass explicit offline flags so air-gapped targets do not attempt network metadata refreshes: `--disablerepo='*'` for `yum`/`dnf`, `--no-network` for `apk`, and `--no-download` for `apt-get install -f`.
- Unified `opf-cli` over `internal/app.Service`, fixing `--temp-dir` being ignored with `--fetch-method docker` and ensuring Package diff approval decisions are persisted consistently.
- Decoupled pipeline steps 4 (*resolve dependencies*) and 5 (*download packages*) via `StageObserver`, so step progress and failure attribution accurately reflect the active stage.
- Aligned `CompareVersions` non-digit ordering with dpkg `verrevcmp` semantics and localized the `ParseVersionReportMode` error message.
- Deduplicated artifact filename validation and shell quoting across APT, YUM/DNF and APK installers.

## [0.3.0] - 2026-11-23

Package diff and installed-version reports.

### Added

- **Package diff** screen between **Packages and Pre-check** and **Pipeline**.
  It is shown only when at least one requested package or dependency will be
  upgraded, not for brand-new packages.
- **Coletar versões do pacote** selector with `Todos` (full installed
  inventory), `Default` (affected packages and dependencies) and `Nenhum`
  (no collection and no Package diff). Collection requires SSH and runs before
  any package download.
- Candidate versions are resolved from repository metadata (`apt-cache policy`,
  `dnf repoquery`/`repoquery`, `apk fetch --simulate`) without downloading the
  packages; installed versions come from the SSH destination.
- **About dialog** in the GUI header, next to Settings, with the OPF logo,
  version, build/commit, release date, developer, GitHub, email, LinkedIn and
  GPL-3.0 license.
- **CLI** flag `--version-report all|default|none`; when upgrades exist and
  `--install` is requested, the CLI prints the jump and asks for confirmation.
- Reports now include the installed-package inventory, the **Package diff**
  table, a readable `De pacote X versão A para versão B` line per upgrade and
  the user decision (`approved` or `aborted`). An aborted decision keeps the
  collected versions in the report and finalizes the session as aborted.
- Pipeline pre-check now compares versions: a package already installed is kept
  for download/install when a newer candidate exists.

### Fixed

- Previewed sessions now reload the stored target metadata before validation,
  so a Package diff → Proceed run reaches the installer with the detected
  target OS/version instead of failing with `SO de destino não informado`
  (`target OS not provided`).

### Changed

- New visual identity: the OPF mascot (golden retriever with the OPF cap and a Linux package box) replaces the previous box/lock icon. The 1600 px master, multi-size `.ico`, `.icns`, Linux 256 px desktop icon, Fyne embedded icon and Windows `.syso` resources were regenerated from `images/OPF_Icon.png`; the editable vector is kept in `images/OPF_Icon.svg`.
- The GUI, TUI and CLI default to `default` collection when SSH is configured;
  without SSH the mode is forced to `none`.
- New sessions persist the version preview in the `preview_json` session column
  (SQLite migration is automatic).

## [0.2.8] - 2026-11-21

Installer-only releases and two Windows installer editions.

### Changed

- Releases now publish **installers only**. The loose CLI/TUI/GUI binaries
  (`opf-cli-*`, `opf-tui-*`, `opf-gui-*`) are no longer attached; the eight
  installers and `SHA256SUMS` are the complete asset set.
- **Windows CLI/TUI installer** (`opf-CLI-installer-windows-*.zip`): raw Go
  wizard with **Bundle (TUI + CLI)**, **TUI only** and **CLI only**; no GUI.
- **Windows full installer** (`opf-Full-installer-windows-*.zip`): graphical
  **Inno Setup** wizard with **Bundle (GUI + TUI + CLI)**, **GUI only**,
  **TUI only** and **CLI only**, same folder/PATH/shortcut/database/launch
  behavior and Apps & Features uninstaller.
- Linux `.run` and macOS `.pkg` installers remain the release deliverables for
  those platforms.

### Notes

- The full Inno Setup installer is a packed executable; Cortex XDR hosts may
  require code signing or an allow-list. The CLI/TUI installer remains raw and
  is accepted without that step.

## [0.2.7] - 2026-11-21

Uninstaller and application-icon fixes.

### Fixed

- **Windows uninstaller**: the installer now ships `uninstall-opf.exe`, writes
  `install.json` and registers an entry in **Apps & Features**. The uninstaller
  removes the binaries from the chosen folder (including `Program Files`), the
  user PATH entry, the Start Menu shortcut and the registry entry, and asks
  whether to remove the local database/history.
- The uninstaller copies itself to `%TEMP%` before removing the installed copy,
  avoiding the Windows file lock on a running executable.
- **Icons**: `opf-cli.exe`, `opf-tui.exe`, `opf-gui.exe`, `opf-installer.exe`
  and `uninstall-opf.exe` now embed the OPF icon in their PE resources; the
  Fyne GUI also sets the window/app icon, so the icon appears in the Windows
  taskbar.
- macOS app bundles include `opf.icns`; the Linux desktop entry installs and
  references the hicolor icon.

### Notes

- Installer assets remain raw Go/shell/pkg payloads; Cortex XDR accepts them.

## [0.2.6] - 2026-11-21

Installer UX and database-location improvements.

### Changed

- **Windows installer is now a guided wizard**: choose the profile, enter the
  installation folder (default `C:\Program Files\OPF`), decide whether to
  create a Start Menu shortcut, review clear progress steps and optionally
  open the GUI or TUI at the end.
- If the chosen folder needs administrator rights, the installer offers to
  restart with UAC or to pick a folder under the user profile.
- The Windows installer adds the chosen folder to the **user PATH**, creates
  the database at `%LocalAppData%\OPF\data\opf.db` and shows a final summary.
- The **GUI default database** moved to `%LocalAppData%\OPF\data\opf.db` on
  Windows, `~/Library/Application Support/OPF/data/opf.db` on macOS and
  `$XDG_DATA_HOME/OPF/data/opf.db` on Linux.
- The **Linux `.run`** installer now prompts for the installation folder,
  offers the desktop shortcut, creates the database, shows progress and can
  open the GUI or TUI.
- The **macOS `.pkg`** postinstall creates the per-user database for the
  console user.
- CLI/TUI installer messages use boxes, numbered steps, defaults and
  yes/no prompts designed for non-technical users.

### Notes

- Installer assets remain raw Go/shell/pkg payloads (no packed Windows setup
  and no MSI), so Cortex XDR accepts them.
- macOS packages keep Apple's fixed `/Applications` and `/usr/local/bin`
  locations; the macOS installer is a native `.pkg`, not a folder wizard.

## [0.2.5] - 2026-11-21

Native installers for every supported OS and CPU, with four installation
profiles.

### Added

- **Installers** for Linux (`.run`), Windows (`.zip` with a raw Go menu
  installer + payload) and macOS (`.pkg`), for **amd64** and **arm64**.
- Each installer offers **bundle** (GUI + TUI + CLI), **GUI only**, **TUI only**
  and **CLI only**.
- Windows installer copies the raw binaries to `%LocalAppData%\OPF`, adds the
  folder to the per-user `PATH` and creates a Start Menu GUI shortcut. It uses
  no packed setup and no `msiexec`, so Cortex XDR accepts it.
- Linux `.run` installs to `/usr/local/bin` as root or `~/.local/bin`
  otherwise and creates a GUI desktop entry.
- macOS `.pkg` installs `OPF.app` to `/Applications` and CLI/TUI to
  `/usr/local/bin`, with four installer choices.
- **Windows arm64 CLI/TUI and GUI**, and **Linux arm64 GUI** artifacts.
- Release now publishes 25 assets (12 CLI/TUI, 6 GUI, 6 installers and
  `SHA256SUMS`).

### Changed

- Windows installation is now per-user and does not require elevation.
- macOS package is unsigned; use **Open** from the context menu if Gatekeeper
  blocks it.

### Notes

- The functional application code is unchanged from v0.2.4 except the TUI
  version banner; this release adds packaging and installers.

## [0.2.4] - 2026-11-21

GUI SQLite path fix.

### Fixed

- The GUI created its SQLite database next to the process working directory.
  When launched from a read-only folder (for example a temporary extraction
  directory), SQLite failed and the UI showed **SQLite indisponível**. The GUI
  now defaults to `<UserConfigDir>/opf/opf.db` (on Windows,
  `%AppData%\opf\opf.db`) and `<home>/OPF` for output.
- If a database path configured in **Configurações Globais** cannot be opened,
  the GUI falls back to the per-user default and shows a warning instead of
  staying unusable.
- Added regression tests for the default paths and for the fallback.

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
