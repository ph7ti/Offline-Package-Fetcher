[English](MANUAL.md) | [Português (BR)](MANUAL.pt-BR.md)

# OPF User Manual — v0.1.0

**Offline Package Fetcher (OPF) 0.1.0** — download Linux packages and their complete dependency closure for isolated environments, transfer them over SSH/SFTP and install them on the target after explicit confirmation.

## Table of contents

**Contents:** [1. Audience and scope](#1-audience-and-scope) · [2. Core concepts](#2-core-concepts) · [3. Prerequisites and platform matrix](#3-prerequisites-and-platform-matrix) · [4. Installation, build and verification](#4-installation-build-and-verification) · [5. Quick start](#5-quick-start) · [6. GUI walkthrough](#6-gui-walkthrough) · [7. TUI walkthrough](#7-tui-walkthrough) · [8. CLI reference](#8-cli-reference) · [9. Remote target detection](#9-remote-target-detection) · [10. Dependency resolution and optional packages](#10-dependency-resolution-and-optional-packages)

[11. Additional repositories](#11-additional-repositories) · [12. SSH: authentication, known_hosts, jump hosts and sudo](#12-ssh-authentication-known_hosts-jump-hosts-and-sudo) · [13. 13-stage pipeline, cancellation and resume](#13-13-stage-pipeline-cancellation-and-resume) · [14. SQLite database](#14-sqlite-database) · [15. Reports and Syslog](#15-reports-and-syslog) · [16. Troubleshooting](#16-troubleshooting) · [17. Hardening checklist](#17-hardening-checklist) · [18. FAQ](#18-faq) · [19. Glossary](#19-glossary) · [20. Version and license](#20-version-and-license)

---

## 1. Audience and scope

This manual targets infrastructure professionals — system administrators, platform engineers and support teams — who must move Linux packages into networks where the target machine cannot reach a package repository. It assumes familiarity with package managers (`apt`, `yum`/`dnf`, `apk`), SSH, Docker and, on Windows, WSL.

In scope:

- the three front ends (`opf-cli`, `opf-tui`, `opf-gui`);
- target selection, dependency resolution and artifact verification;
- SSH/SFTP transfer, jump hosts, remote installation and sudo;
- additional package repositories;
- local persistence, reports and Syslog;
- operational troubleshooting and hardening.

Out of scope:

- configuring package repositories on the target (OPF never writes repository configuration on the destination);
- distributing arbitrary files (OPF handles package artifacts only);
- multi-target sessions (one session targets exactly one distribution, version and architecture);
- repository authentication (not available in 0.1.0).

## 2. Core concepts

**Session (`sessão`).** One pipeline execution recorded in SQLite with an increasing numeric ID, storing target, packages, fetch method, host, stage progress, artifacts and events.

**Target.** A distribution, version and architecture, expressed as `distro:versao` (for example `ubuntu:22.04`, `centos:7`, `alpine:3.20`) plus an architecture such as `amd64` or `x86_64`. The target decides the package family, the isolated image/distro and the installer.

**Package family.** `apt` for Ubuntu/Debian (`.deb`), `rpm` for CentOS/RHEL/Rocky Linux/AlmaLinux/Fedora (`.rpm`) and `apk` for Alpine (`.apk`).

**Isolated environment.** An ephemeral Docker container (all families) or a registered WSL distribution (Windows, APT only) created from the same distribution and version as the target; resolution and downloads run inside it, not on the target.

**Fetch method.** `docker` or `wsl`; the GUI and TUI also accept `auto` (WSL for APT on Windows, Docker otherwise). The CLI accepts only `docker` and `wsl`.

**Root package vs dependency.** Every package passed with `--pkg` is a *root* (reported as `principal`); everything else resolved from it is a *dependency* (reported as `dependente`).

**Artifact.** A downloaded package file plus metadata (name, version, architecture, SHA-256), copied to `./opf-output/sessao-<id>/` and listed in `SHA256SUMS`.

**Pre-check, checkpoint and P1/P2/P3.** The pre-check (only with SSH) records packages already installed; checkpoints persist per-stage state for a safe resume; P1/P2/P3 are the SHA-256 checkpoints inside the environment, on the host and on the target.

## 3. Prerequisites and platform matrix

### 3.1 Requirements by resource

| Component | Requirement |
|:---|:---|
| Go | 1.27.4 to build from source (`go.mod` declares `go 1.27.4`) |
| Docker | Docker Engine (Linux) or Docker Desktop (Windows/macOS) with a reachable daemon for `--fetch-method docker`; images are pulled at run time |
| WSL | Windows 10/11 with WSL2, a registered Ubuntu/Debian distribution and `root` access for `--fetch-method wsl` |
| SSH | SSH server on the target, an account with SFTP access, host key present in `known_hosts` |
| Sudo | When the SSH user is not `root` and remote installation is requested, the account password must be available (sent to `sudo -S` through stdin) |
| GUI toolchain | `gcc` plus graphics libraries: Linux `libgl1-mesa-dev` + `xorg-dev`, Windows mingw-w64, macOS Xcode Command Line Tools |
| Network | The host running OPF needs access to the package repositories (and to the registries used for images); the target does not |

### 3.2 Platform matrix

| Platform | CLI | TUI | GUI | Docker fetch | WSL fetch | Notes |
|:---|:---:|:---:|:---:|:---:|:---:|:---|
| Linux (amd64/arm64) | yes | yes | yes (CGO) | yes | no | primary platform |
| Windows (amd64) | yes | yes | yes (CGO/MinGW) | yes | yes (APT only) | WSL requires a registered distro |
| macOS (amd64/arm64) | yes | yes | yes (CGO/Xcode CLT) | yes | no | secondary platform; no macOS targets |

Target matrix:

| Family | `--target` values | Manager used in the environment | Installer on the target |
|:---|:---|:---|:---|
| APT | `ubuntu`, `debian` | `apt-get update` + `apt-cache depends --recurse` + `apt-get download` | `dpkg -i`, falling back to `apt-get install -f -y` |
| RPM | `centos`, `rhel`, `rockylinux`, `almalinux`, `fedora` | `dnf download --resolve --alldeps` or `repoquery` + `yumdownloader` | `dnf localinstall -y` or `yum localinstall -y` |
| APK | `alpine` | `apk fetch --recursive` | `apk add --allow-untrusted` |

Architecture normalization:

| Input | APT | RPM | APK |
|:---|:---|:---|:---|
| `x86_64`, `amd64`, `x64` | `amd64` | `x86_64` | `x86_64` |
| `aarch64`, `arm64` | `arm64` | `aarch64` | `aarch64` |
| `armv7l`, `armv7`, `armhf` | `armhf` | `armv7hl` | `armv7` |
| `i686`, `i386` | `i386` | `i686` | `i686` |

Docker platform mapping used by the fetcher: `amd64` → `linux/amd64`, `arm64` → `linux/arm64`, `armhf` → `linux/arm/v7`, `i386` → `linux/386`, `ppc64el` → `linux/ppc64le`, `s390x` → `linux/s390x`.

## 4. Installation, build and verification

### 4.1 Release binaries

Release assets are produced by the release workflow from the tagged commit. For v0.1.0 the published set is:

| Asset | Description |
|:---|:---|
| `opf-cli-linux-amd64`, `opf-cli-linux-arm64` | static CLI (`CGO_ENABLED=0`) |
| `opf-cli-windows-amd64.exe`, `opf-cli-darwin-amd64`, `opf-cli-darwin-arm64` | static CLI for the remaining platforms |
| `opf-tui-*` (same five platform variants) | static TUI (`CGO_ENABLED=0`); the Windows asset ends in `.exe` |
| `opf-gui-linux-amd64` | Linux GUI (`CGO_ENABLED=1`, needs the graphics libraries) |
| `SHA256SUMS` | SHA-256 digest of every attached asset |

Verify the download before running it:

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

```powershell
Get-FileHash .\opf-cli-windows-amd64.exe -Algorithm SHA256   # compare with the published SHA256SUMS
```

### 4.2 Build from source

Linux/macOS:

```bash
git clone https://github.com/ph7ti/Offline-Package-Fetcher.git
cd Offline-Package-Fetcher
make build        # bin/opf-cli
make build-tui    # bin/opf-tui
make build-gui    # bin/opf-gui (CGO_ENABLED=1 + graphics libraries)
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w" -o bin/opf-cli.exe ./cmd/opf-cli
go build -trimpath -ldflags "-s -w" -o bin/opf-tui.exe ./cmd/opf-tui
$env:CGO_ENABLED = '1'   # requires gcc/mingw-w64 in PATH
go build -trimpath -ldflags "-s -w" -o bin/opf-gui.exe ./cmd/opf-gui
```

GUI prerequisites (Fyne v2.8):

- Debian/Ubuntu: `sudo apt-get install gcc libgl1-mesa-dev xorg-dev` (the CI job also installs `libxkbcommon-dev` and `libwayland-dev`, required by GLFW 3.4)
- Fedora: `sudo dnf install gcc mesa-libGL-devel xorg-x11-server-devel libXcursor-devel libXrandr-devel libXinerama-devel`
- Windows: mingw-w64 `gcc` on `PATH`
- macOS: `xcode-select --install`

Without CGO, `opf-gui` compiles to a stub that prints `opf-gui requer CGO_ENABLED=1 e as bibliotecas gráficas do Fyne; use make build-gui` and exits with status 1.

### 4.3 Post-installation verification

```bash
opf-cli -h        # lists every flag and default
go version        # should report the toolchain used for the build
opf-tui           # opens the main menu (Ctrl+C exits)
opf-gui           # opens the window; without CGO it exits with a guidance message
```

`opf-cli -h` ends with `opf-cli: flag: help requested` and exit code 1; this is expected, because Go's standard `flag` package signals help as an error.

A Docker-backed smoke test (`opf-cli --target ubuntu:22.04 --arch amd64 --pkg vim --out ./opf-output`) validates binary, SQLite, fetcher and manifest end to end and creates `./opf.db`, `./opf-output/sessao-<id>/` and `SHA256SUMS`.

### 4.4 Global settings (GUI and TUI)

The GUI and the TUI store non-sensitive preferences as JSON:

| Platform | Path |
|:---|:---|
| Linux | `~/.config/opf/config.json` |
| Windows | `%AppData%\opf\config.json` |
| macOS | `~/Library/Application Support/opf/config.json` |

The directory is created with mode `0700` and the file is published atomically with mode `0600`.

| Field | Type | Default | Purpose |
|:---|:---|:---|:---|
| `fetch_method` | string | `docker` | `docker`, `wsl` or `auto` |
| `temp_dir` | string | empty | base directory for host temporaries |
| `database_path` | string | empty | SQLite path |
| `stage_timeout_seconds` | int | `600` | per-stage timeout |
| `docker_image` | string | empty | default image |
| `wsl_distro` | string | empty | default WSL distribution |
| `include_optional` | bool | `false` | include optional dependencies |
| `force_reinstall` | bool | `false` | force reinstallation |
| `syslog_enabled` | bool | `false` | enable Syslog |
| `syslog_host` | string | empty | collector |
| `syslog_port` | int | `514` (UI default) | port |
| `syslog_proto` | string | `udp` | `udp` or `tcp` |
| `syslog_tag` | string | `opf` | RFC 5424 tag |

The CLI **does not** read this file: every parameter comes from flags and environment variables.

## 5. Quick start

### 5.1 Download only

```bash
opf-cli --target ubuntu:22.04 --arch amd64 --pkg vim --pkg git --out ./opf-output
```

The files land in `./opf-output/sessao-<id>/` next to `SHA256SUMS`; the record is stored in `./opf.db`. The console output format is documented in [section 8.10](#810-standard-cli-output).

### 5.2 Transfer over SSH with automatic target detection

```bash
opf-cli --pkg postgresql \
  --ssh-host db01.internal --ssh-user deploy \
  --ssh-key ~/.ssh/id_ed25519
```

Without `--target`/`--arch`, OPF connects, reads `/etc/os-release` and `uname -m`, derives `distro:version`, architecture and the recommended image, and then runs the pipeline.

### 5.3 Transfer and install with sudo

```bash
export OPF_SSH_PASSWORD='account-password'

opf-cli --pkg nginx --ssh-host web01.internal --ssh-user deploy \
  --ssh-key ~/.ssh/id_ed25519 --install --sudo --yes \
  --export-json ./opf-output/report.json \
  --export-markdown ./opf-output/report.md
```

Without `--yes`, OPF asks `Instalar 34 pacotes em deploy@web01.internal? [y/N]` and only `y`/`yes` proceeds. `--yes` still requires `--install`. Prefer `OPF_SSH_PASSWORD` over `--ssh-password`: command-line arguments are visible to other local users.

### 5.4 Windows with WSL

```bash
opf-cli --target ubuntu:22.04 --arch amd64 --pkg htop \
  --fetch-method wsl --wsl-distro Ubuntu-22.04
```

WSL fetches only APT families (Ubuntu/Debian); RPM and APK require Docker. Run this from PowerShell or Windows Terminal; the distribution must be registered and accessible to the current user.

## 6. GUI walkthrough

Start it with `opf-gui` (or `bin/opf-gui`). The window is titled **OPF — Offline Package Fetcher**. Every screen shares a top bar (icon, product name, Home/History/Settings shortcuts, theme toggle) and a status bar (pipeline state, database file, current theme).

### 6.1 Início

Quick actions: **Nova sessão**, **Continuar**, **Histórico**, **Configurações**, **Atualizar**. Four counters (sessions, packages, installed packages, SQLite file) and up to four recent session cards with **Relatório**, **Retomar** and **Arquivar** actions.

### 6.2 Conexão SSH e Jump Hosts

The **Modo de operação** toggle (**Enviar e instalar via SSH**) hides or reveals the connection (**Conexão**: host, port, user, key, password, known hosts), the ordered jump hosts with per-hop authentication and the remote directory (**Pontes SSH e destino remoto**), and the security card (**Detecção e segurança**) with **Ignorar verificação de host key (inseguro)**, its red warning banner, **Detectar destino** and **Testar conexão**.

**Detectar destino** reads `/etc/os-release` and `uname -m` and fills distribution, version, architecture and image; the detection is invalidated if host, user, port or the jump list changes. **Configurar manualmente** switches to the session configuration screen without detection. **Continuar** requires host and user, a key or password, and a detected or manually configured target.

### 6.3 Configuração da Sessão (advanced)

**Distro de destino** (Ubuntu, Debian, CentOS, Rocky Linux, AlmaLinux, Fedora, RHEL, Alpine), **Versão**, **Arquitetura**, **Método de fetch** (Docker, WSL, Automático), **Imagem Docker (opcional)**, **Distro WSL (opcional)**, **Diretório de saída**, **Incluir dependências opcionais** and the list of **Repositórios adicionais** (used only inside the download environment). **Continuar** validates that distro, version and architecture are filled.

### 6.4 Pacotes e Pré-check

Package entry (one per line or comma-separated), optional dependencies, **Forçar reinstalação (ignorar pré-check)**, **Instalar após transferir e validar**, **Usar sudo com a senha da conta** and the sudo password. With SSH disabled, the install card is hidden and the screen states that packages will only be downloaded and validated locally. **Iniciar** runs the session.

### 6.5 Pipeline

Progress bar, 13 stage indicators, live log (last 400 lines) and **Cancelar execução**. The remote pre-check, integrity verification and installation are stages of this screen — the GUI does not have separate screens for them. When installation is requested, the dialog **Confirmar instalação** offers **Instalar** / **Recusar**; refusing keeps the artifacts uploaded.

### 6.6 Resultado

One of five outcomes: installation completed, transfer completed (installation declined), nothing to install (all packages already present), session completed (download/transfer only) or session ended with error. Shows session ID, artifact count, destination, installation state, local output directory, manifest path, remote directory and the list of packages that were already installed. Actions: **Ver relatório**, **Histórico**, **Nova sessão**.

### 6.7 Relatório da Sessão

Markdown report rendered from SQLite. **Exportar JSON** writes `opf-relatorio-<id>.json` and **Exportar Markdown** writes `opf-relatorio-<id>.md` to the configured output directory (mode `0600`); **Enviar ao Syslog** sends the report as a structured event (requires Syslog enabled); **Voltar** returns to the previous screen.

### 6.8 Histórico

Every non-archived session as a card with status badge and actions **Relatório**, **Retomar**, **Arquivar** and **Atualizar**. Archiving is logical (`arquivada = 1`) and removes the session from the default listings; concluded sessions cannot be resumed.

### 6.9 Configurações Globais

| Item | Effect |
|:---|:---|
| **Arquivo SQLite** | database used by history, checkpoints and reports |
| **Diretório temporário** | base for host temporaries during prepare |
| **Timeout de etapa (segundos)** | default `600` (10 minutes) |
| **Syslog: Habilitar, Host, Porta, Protocolo, Tag** | asynchronous RFC 5424 delivery (UDP/TCP) |

**Salvar configurações** validates the timeout (`> 0`), the port (`1..65535`) and the host when Syslog is enabled, writes `config.json` with mode `0600`, reopens the database and shows a confirmation dialog. Paths and file contents are described in [section 4.4](#44-global-settings-gui-and-tui).

## 7. TUI walkthrough

The TUI (`opf-tui`) is pure Bubble Tea (`CGO_ENABLED=0`) and offers the same operations as the GUI, using the alternate screen and a context-sensitive help line.

### 7.1 Main menu

```
Offline Package Fetcher

> Nova sessão
  Continuar sessão
  Histórico
  Configurações globais
  Sair
```

Navigation: `↑`/`↓` or `k`/`j`; `Enter` selects; `q` exits. The footer confirms `SQLite: OK` and the policy line `Sem NOPASSWD • host key obrigatória • segredos nunca são logados`.

### 7.2 Screens and shortcuts

| Screen | Keys | Notes |
|:---|:---|:---|
| **Configuração da Sessão** | `tab`/`↓`, `shift+tab`/`↑`, `space` (toggles), `enter` advances, `ctrl+r` repositories, `esc` back | distro, version, architecture, fetch (`docker`/`wsl`/`auto`), image, WSL distro, output, SQLite, include optional |
| **Repositórios adicionais** | `↑`/`↓`, `a` add, `e`/`enter` edit, `d` remove, `esc` back | used only inside the isolated environment |
| **Repositório adicional** (form) | `tab`/`↓`, `space` cycles choices, `ctrl+s` or `enter` saves, `esc` discards | name, URL, key source, key value, CA, APT suite/components/flat, insecure TLS |
| **Conexão SSH e Jump Hosts** | `tab`/`↓`, `space` toggle, `ctrl+j` jumps, `ctrl+d` detect, `ctrl+t` test, `ctrl+a` advanced configuration, `enter` detect/continue, `esc` back | host key is strict by default; the skip toggle is equivalent to `StrictHostKeyChecking=no` |
| **Pontes SSH** | `↑`/`↓`, `a` add, `e`/`enter` edit, `d` remove, `K`/`J` move, `esc` back | passwords live only in memory |
| **Ponte SSH** (form) | `tab`/`↓`, `space` cycles authentication, `ctrl+s` or `enter` saves, `esc` discards | empty known_hosts inherits the destination file |
| **Pacotes e Pré-check** | `tab`/`↓`, `space` toggles, `enter` starts, `esc` back | packages, optional, force reinstall, install, sudo, sudo password |
| **Executando pipeline** | `c` cancels, `y`/`s` confirms installation, `n`/`esc` declines, `ctrl+c` quits | 13 stages and the last log lines are visible |
| **Resultado** | `r` report, `h` history, `n` new session, `esc`/`q` welcome | shows session, artifacts, output, manifest and remote directory |
| **Relatório da sessão** | `↑`/`↓`, `pgup`/`pgdown`, `J` JSON, `M` Markdown, `S` Syslog, `esc` back | rendered from SQLite, works for old sessions |
| **Histórico** | `↑`/`↓`, `enter` report, `r` resume, `a` archive, `esc` back | archived sessions are hidden |
| **Configurações Globais** | `tab`/`↓`, `space` toggle, `ctrl+s` saves, `esc` back | temp dir, timeout, Syslog host/port/protocol/tag |

Notes:

- `ctrl+c` quits at any time and cancels a running pipeline.
- Toggles are changed with `space`; `enter` advances/saves.
- If the remote user is `root`, the TUI detects it before installing and disables sudo automatically. If the user is not root and sudo is not selected, the run fails with `usuário remoto não é root; habilite "Usar sudo" e informe a senha da conta`.
- When the pipeline reaches stage 10, the progress screen asks `Instalar N artefatos? [y/n]`.

### 7.3 Recommended TUI flow

1. **Nova sessão** → enable SSH and provide host/user/credential.
2. `ctrl+d` detects the target; confirm the `Destino detectado: ...` line.
3. `ctrl+j` adds jump hosts if needed; `ctrl+t` tests the connection.
4. `enter` advances from the SSH screen → **Pacotes e Pré-check**: enter packages, mark installation/sudo if applicable.
5. `enter` starts the run; follow the stages; confirm installation with `y` when asked.
6. On **Resultado**, `r` opens the report; `J`/`M`/`S` export it or send it to Syslog.

Validation errors appear in red at the bottom of the screen; `esc` always returns without executing anything.

## 8. CLI reference

`opf-cli` uses Go's standard `flag` package: single-dash and double-dash forms are both accepted (`-target` and `--target`), and flags must precede positional arguments. No positional arguments are accepted (`argumentos inesperados: ...`). All flag help texts and runtime messages are in Brazilian Portuguese in 0.1.0.

### 8.1 Target and output

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--target` | string | empty | `distro:versao` (`ubuntu`, `debian`, `centos`, `rhel`, `rockylinux`/`rocky`, `almalinux`, `fedora`, `alpine`). Required unless SSH target detection runs |
| `--arch` | string | empty | target architecture (`amd64`, `x86_64`, `arm64`, `aarch64`, `armhf`, `armv7l`, `i386`, `i686`, `ppc64el`, `s390x`). Required unless detection runs |
| `--out` | string | `./opf-output` | cannot be empty; artifacts go to `<out>/sessao-<id>` |
| `--db` | string | `./opf.db` | cannot be empty; created with schema and automatic migrations |
| `--export-json` | string | empty | path of the JSON report |
| `--export-markdown` | string | empty | path of the Markdown report |

Reports are generated from SQLite after the pipeline finishes, only when an export flag is present. Parent directories are created with mode `0700`, report files with `0600`.

### 8.2 Packages and selection

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--pkg` | repeatable / list | *(required)* | at least one; repeat the flag or separate values with commas. APT names may carry an architecture qualifier (`libssl3:amd64`) |
| `--with-optional` | bool | `false` | includes Recommends/Suggests/Enhances (APT) and weak dependencies (DNF); ignored by APK |
| `--force-reinstall` | bool | `false` | ignores the pre-check result and reinstalls packages already present |

Names are validated by each resolver: APT `^[a-z0-9][a-z0-9+.-]+(?::[a-z0-9][a-z0-9-]*)?$`, RPM/APK `^[A-Za-z0-9][A-Za-z0-9+._-]*$`. Spaces, semicolons, backticks, `$()` and path separators are rejected before anything runs.

### 8.3 Fetch and environment

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--fetch-method` | string | `docker` | `docker` or `wsl`; `wsl` accepts APT families only |
| `--image` | string | empty | overrides the Docker image; not applicable to `wsl` |
| `--wsl-distro` | string | empty | registered distribution; only meaningful with `--fetch-method wsl` |
| `--temp-dir` | string | empty (OS default) | base directory for host temporaries; in the CLI it applies only to the WSL fetcher (Docker keeps the OS default) |

### 8.4 SSH (destination)

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--ssh-host` | string | empty | enables SSH/SFTP, pre-check, host info and installation |
| `--ssh-port` | int | `22` | validated within `1..65535` |
| `--ssh-user` | string | empty | required with `--ssh-host` |
| `--ssh-key` | string | empty | private key path; accepts `~/...` |
| `--ssh-password` | string | empty | SSH password (prefer `OPF_SSH_PASSWORD`); also authenticates `sudo -S` |
| `--ssh-known-hosts` | string | `~/.ssh/known_hosts` | file must exist and be a regular file (unless host-key checking is disabled) |
| `--ssh-insecure-skip-host-key` | bool | `false` | insecure; equivalent to `StrictHostKeyChecking=no` on every hop |
| `--remote-dir` | string | `/tmp/opf-sessao-<id>` | remote session directory |

Every SSH flag (including jump-host flags) requires `--ssh-host`; using one without it is a validation error. With `--ssh-host`, `--ssh-user` is mandatory and at least one of `--ssh-key` or a password must be provided (`informe --ssh-key ou --ssh-password/OPF_SSH_PASSWORD`).

### 8.5 SSH jump hosts

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--ssh-jump` | repeatable | — | `[user@]host[:port]`; the order is the hop order. IPv6 requires brackets |
| `--ssh-jump-auth` | repeatable | — | mandatory per hop: `password`, `key` or `key+password` |
| `--ssh-jump-password` | repeatable | — | hop password; conflicts with `OPF_SSH_JUMP_PASSWORDS` |
| `--ssh-jump-key` | repeatable | — | hop private key |
| `--ssh-jump-known-hosts` | repeatable | inherits `--ssh-known-hosts` | hop-specific known_hosts; empty inherits |

The lists are aligned by index with `--ssh-jump`; mismatched counts are validation errors. Environment variables:

| Variable | Content |
|:---|:---|
| `OPF_SSH_PASSWORD` | destination SSH password, also used by `--sudo` when no `--ssh-password` is given |
| `OPF_SSH_JUMP_PASSWORDS` | JSON array of strings with one password per jump, aligned with `--ssh-jump` order |

`--ssh-jump-password` and `OPF_SSH_JUMP_PASSWORDS` are mutually exclusive (`use --ssh-jump-password ou OPF_SSH_JUMP_PASSWORDS, não ambos`), and the environment array length must match the number of jumps.

### 8.6 Sudo and installation

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--install` | bool | `false` | installs on the target after upload and remote checksum verification; requires `--ssh-host` |
| `--yes` | bool | `false` | answers yes to the installation confirmation; requires `--install` |
| `--sudo` | bool | `false` | runs the installation through `sudo -S` with the SSH account password; requires `--install` and a password (`--sudo exige --ssh-password/OPF_SSH_PASSWORD ...`) |

Without `--yes`, stage 10 prints `Instalar N pacotes em usuario@host? [y/N]` and reads stdin; anything but `y`/`yes` declines. A declined installation ends the session successfully with packages in status `enviado_nao_instalado`.

The CLI never probes whether the SSH user is root. Use `--sudo` whenever the account is not root; when it is root, omit `--sudo` (sudo would still work if the password is valid, but it is unnecessary).

### 8.7 Additional repositories

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--repo-config` | string | empty | JSON file with an array of repository objects; mutually exclusive with the `--repo-*` flags |
| `--repo-url` | string | empty | URL of one additional repository (simple mode); required when any other `--repo-*` flag is used |
| `--repo-name` | string | `private` | repository name in simple mode (letters, digits, space, `.`, `_`, `-`; max 64) |
| `--repo-family` | string | target family | `apt`, `rpm` or `apk`; must match the target family |
| `--repo-key` | string | empty | local key file |
| `--repo-key-url` | string | empty | key download URL (mutually exclusive with `--repo-key`) |
| `--repo-ca` | string | empty | custom CA file |
| `--repo-apt-suite` | string | empty | APT suite/distribution (`jammy`, `bookworm`, ...); required for non-flat APT |
| `--repo-apt-components` | string | `main` | comma-separated APT components |
| `--repo-apt-flat` | bool | `false` | flat APT repository (no suite/components) |
| `--repo-insecure-tls` | bool | `false` | skips TLS verification for this repository (insecure) |
| `--repo-insecure-gpg` | bool | `false` | skips GPG signature verification for this repository (insecure) |

`--repo-config` is limited to 1 MiB, must be a regular file and rejects unknown JSON fields. Relative `key_file`/`ca_file` paths are resolved against the configuration file directory.

### 8.8 Syslog

| Flag | Type | Default | Notes |
|:---|:---|:---|:---|
| `--syslog` | bool | `false` | sends session events to an external Syslog; requires `--syslog-host` |
| `--syslog-host` | string | empty | Syslog host |
| `--syslog-port` | int | `514` | Syslog port (1–65535) |
| `--syslog-proto` | string | `udp` | `udp` or `tcp` |
| `--syslog-tag` | string | `opf` | RFC 5424 APP-NAME (`[A-Za-z0-9][A-Za-z0-9._-]*`) |

Delivery is best-effort; SQLite remains the source of truth (see [section 15.3](#153-external-syslog)).

### 8.9 CLI validation errors

| Error | Cause | Fix |
|:---|:---|:---|
| `--pkg é obrigatório` | no package informed | use `--pkg <name>` |
| `--yes exige --install` / `--sudo exige --install` | confirmation flags without installation | add `--install` |
| `--install exige --ssh-host` | installation without a destination | provide `--ssh-host` and `--ssh-user` |
| `--syslog exige --syslog-host` | Syslog without a collector | provide `--syslog-host` |
| `--<flag> exige --ssh-host` | SSH flag without a destination | remove the flag or provide `--ssh-host` |
| `--ssh-user é obrigatório com --ssh-host` | missing user | provide `--ssh-user` |
| `informe --ssh-key ou --ssh-password/OPF_SSH_PASSWORD` | no credential | provide a key or password |
| `--target e --arch são obrigatórios sem --ssh-host` | download without target or SSH | provide both |
| `--target inválido: "x" (esperado distro:versao)` | malformed target | use `distro:versao` |
| `--fetch-method inválido: "x" (use docker ou wsl)` | invalid method | use `docker` or `wsl` |
| `--wsl-distro exige --fetch-method wsl` | WSL distro without the method | add `--fetch-method wsl` |
| `--repo-config é mutuamente exclusivo com as flags --repo-*` | both repository forms | choose one |
| `--repo-url é obrigatório com as flags --repo-*` | simple mode without URL | provide `--repo-url` |
| `--ssh-jump exige --ssh-jump-auth ...` | hop without authentication | provide `--ssh-jump-auth` |
| `--ssh-jump-auth tem N valores para M pontes` | mismatched counts | align the lists |
| `--sudo exige --ssh-password/OPF_SSH_PASSWORD ...` | sudo without a password | provide the password |
| `argumentos inesperados: ...` | positional argument | remove the argument |

Errors are printed as `opf-cli: <message>` on stderr. Exit codes:

| Code | Meaning |
|:---|:---|
| 0 | session completed (including "installation declined" and "all packages already installed") |
| 1 | validation error, pipeline failure or `-h` |

### 8.10 Standard CLI output

At the end of a successful session:

```
sessão <id> concluída
artefatos: <n>
saída: <local directory>
manifesto: <path to SHA256SUMS>
destino remoto: <remote directory>   # only with SSH
relatório JSON: <path>               # only with --export-json
relatório Markdown: <path>           # only with --export-markdown
instalação: concluída                # only when installation was accepted
instalação: recusada; pacotes permanecem enviados   # when declined
```

## 9. Remote target detection

When `--target` and/or `--arch` are omitted with `--ssh-host`, or when the GUI/TUI click **Detectar destino** / press `ctrl+d`:

1. a transient SSH connection is opened (45 s probe timeout; 60 s in the CLI);
2. `cat /etc/os-release` and `uname -m` run on the target;
3. OPF normalizes `ID`/`ID_LIKE` to a supported distribution;
4. `VERSION_ID` (or `VERSION`) defines the version and `uname -m` defines the architecture;
5. a preset suggests the recommended Docker image.

Distribution normalization (`ID` → internal target): `ubuntu` → `ubuntu`, `debian` → `debian`, `alpine` → `alpine`, `centos` → `centos`, `rhel`/`ol` → `rhel`, `rocky`/`rockylinux` → `rockylinux`, `almalinux` → `almalinux`, `fedora` → `fedora`.

When `ID` is unknown, `ID_LIKE` tokens are consulted in order (`ubuntu`, `debian`, `alpine`, `centos`, `rhel`/`fedora`). If `VERSION_ID` and `VERSION` are both empty the detection fails (`VERSION_ID ausente em /etc/os-release`); an unsupported architecture fails with `arquitetura não suportada` and an unrecognized distribution with `distro não reconhecida`.

Preset image and validation status:

| Distribution | Image used | Marked as validated |
|:---|:---|:---|
| Ubuntu | `ubuntu:<version>` | 22.04, 24.04 |
| Debian | `debian:<version>` | 12 |
| Alpine | `alpine:<version>` | 3.20 |
| CentOS 7 | `centos:7` | yes |
| CentOS 8+ | `quay.io/centos/centos:stream<major>` | 9, 10 |
| Rocky Linux | `rockylinux:<version>` | 9.x |
| AlmaLinux | `almalinux:<version>` | 9.x |
| Fedora | `fedora:<version>` | 41 |
| RHEL | `registry.access.redhat.com/ubi<major>/ubi` | 8.x, 9.x |

If the preset is not marked as validated, the CLI logs `preset do target detectado ainda não foi validado em integração; use configuração avançada se necessário` and the GUI/TUI show a warning. Override the image with `--image` / **Imagem Docker** when necessary.

When detection succeeds, the CLI fills `--target` as `<os>:<version>`, `--arch` and (Docker fetcher, empty `--image`, method not WSL) the preset image. Explicit values always win.

Important: without detection, the Docker fetcher defaults to `<os>:<version>`. That tag does not exist for every distribution (for example CentOS Stream), so either let OPF detect the target or pass `--image` explicitly.

## 10. Dependency resolution and optional packages

Resolution always happens inside the isolated environment, with the target distribution's own tools and repository configuration, so the result matches what the target would install.

Optional dependencies (`--with-optional` / **Incluir dependências opcionais** / **Opcionais**):

| Family | Flag off | Flag on |
|:---|:---|:---|
| APT | `Recommends`, `Suggests`, `Enhances` excluded | included |
| RPM/dnf | `install_weak_deps=False` | weak dependencies included |
| APK | no optional notion | unchanged |

### 10.1 APT (Ubuntu/Debian)

- `apt-get update` runs with isolated `Dir::State::lists`, `Dir::Cache::archives`, `Dir::Cache::pkgcache` and `Dir::Cache::srcpkgcache` under `/tmp/opf-<session>/`, plus `Acquire::Retries=3`.
- Dependencies come from `apt-cache depends --recurse --no-conflicts --no-breaks --no-replaces`.
- By default `--no-recommends --no-suggests --no-enhances` are also passed; `--with-optional` removes them.
- Downloads use `apt-get -o APT::Architecture=<arch> download <pkgs...>`.
- Each root is included in the resolved set; the union of all roots is downloaded once.

### 10.2 RPM (CentOS, RHEL, Rocky Linux, AlmaLinux, Fedora)

- The environment installs `dnf-plugins-core` (dnf) or `yum-utils` (yum) and imports the distribution's `/etc/pki/rpm-gpg` keys.
- With dnf, dependency resolution is delegated to `dnf download --resolve --alldeps --arch=<arch> --destdir=/tmp/opf-<session>`; weak dependencies are disabled with `--setopt=install_weak_deps=False` unless `--with-optional` is used.
- With yum, dependencies come from `repoquery --requires --resolve --recursive --queryformat=%{name}` and downloads from `yumdownloader --resolve --archlist=<arch>`.
- CentOS 7 has EOL repositories, so the fetcher rewrites `/etc/yum.repos.d/CentOS-*.repo` to the `centos-store` mirror and pins `$releasever` to `7.9.2009` before downloading.
- For CentOS/RHEL/Rocky/AlmaLinux, EPEL is enabled on a best-effort basis (including a fallback release RPM for UBI) because several extra packages live there. Failure is logged and does not abort the session.

### 10.3 APK (Alpine)

- `apk --cache-dir <cache> update` prepares the index.
- Dependency listing uses `apk fetch --recursive --simulate --arch=<arch>`, parsed from the `Downloading` lines.
- The download is `apk fetch --recursive --arch=<arch> --output=/tmp/opf-<session>`.

### 10.4 Verification inside the environment (P1)

After downloading, the fetcher builds an inventory with `dpkg-deb --info` (`.deb`), `rpm -K` + `rpm -qp` (`.rpm`) or `apk verify` + `.PKGINFO` (`.apk`) and hashes each file with SHA-256 inside the environment. Then it enforces:

- every resolved package (or, for RPM, every root) has a downloaded artifact, otherwise `pacote resolvido sem artefato baixado: "..."`;
- no duplicate artifact names;
- artifact names contain no path separators and are regular non-empty files;
- the host copy hash equals the hash computed inside the environment (`cópia corrompida do artefato "..."`).

Those in-environment digests become `sha256_p1`; the host digest becomes `sha256_p2` and, after the manifest is generated and verified, `sha256`.

## 11. Additional repositories

Additional repositories are optional and are configured **only inside the isolated download environment**. Nothing is written to the target, and the list is additive: official repositories remain enabled.

### 11.1 JSON schema (`--repo-config`)

```json
[
  {
    "name": "internal",
    "family": "apt",
    "url": "https://repo.internal/ubuntu",
    "key_file": "./keys/internal.asc",
    "ca_file": "./keys/internal-ca.pem",
    "suite": "jammy",
    "components": ["main", "internal"]
  }
]
```

| Field | Type | Required | Notes |
|:---|:---|:---|:---|
| `name` | string | yes | `^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`; duplicates are rejected after slugification |
| `family` | string | no | `apt`, `rpm`, `apk`; defaults to the target family and must match it |
| `url` | string | yes | `http`/`https`, host required, no credentials, no fragment, no control characters/whitespace |
| `key_file` | string | one of key | local key file (max 1 MiB, regular file); relative paths resolve against the JSON file directory |
| `key_url` | string | one of key | key download URL (http/https, no credentials); mutually exclusive with `key_file` |
| `ca_file` | string | no | custom CA file used for TLS |
| `suite` | string | APT non-flat | APT suite/distribution |
| `components` | array | no | APT components; defaults to `["main"]`; invalid for flat/RPM/APK |
| `flat` | bool | no | flat APT repository (no suite/components) |
| `insecure_skip_tls_verify` | bool | no | not supported for APK |
| `insecure_skip_gpg_check` | bool | no | mutually exclusive with key material |

Constraints enforced during normalization:

- `key_file` and `key_url` cannot be combined, and one of them (or `insecure_skip_gpg_check`) is mandatory;
- `ca_file` and `insecure_skip_tls_verify` are mutually exclusive, except for APK where insecure TLS is rejected entirely;
- an `http://` URL cannot declare a CA or insecure TLS (no TLS in use);
- APT non-flat requires a suite; components default to `main`;
- RPM, APK and flat APT reject suite/components/flat where they do not apply;
- `$basearch`-style variables are accepted only for RPM repositories.

### 11.2 Examples

APT:

```bash
opf-cli --target ubuntu:22.04 --arch amd64 --pkg internal-agent \
  --repo-url https://repo.internal/ubuntu \
  --repo-name internal \
  --repo-apt-suite jammy \
  --repo-apt-components main,internal \
  --repo-key ./keys/internal.asc
```

RPM:

```bash
opf-cli --target rockylinux:9 --arch x86_64 --pkg internal-agent \
  --repo-url https://repo.internal/rpm/el9/\$basearch \
  --repo-name internal \
  --repo-key ./keys/RPM-GPG-KEY-internal
```

APK:

```bash
opf-cli --target alpine:3.20 --arch x86_64 --pkg internal-agent \
  --repo-url https://repo.internal/alpine/v3.20/main \
  --repo-name internal \
  --repo-key ./keys/internal.rsa.pub
```

For a flat APT repository, omit the suite and pass `--repo-apt-flat`; the source line becomes `deb [signed-by=...] <url> ./`.

### 11.3 How each family is configured in the environment

- **APT**: `/etc/apt/sources.list.d/opf-<session>-<slug>.list` with `signed-by=` (or `trusted=yes` when GPG is skipped), keyring at `/usr/share/keyrings/opf-<session>-<slug>.gpg` (armored keys are dearmored with `gpg`, installing `gnupg` if missing), and `/etc/apt/apt.conf.d/99opf-<session>-<slug>` for a custom CA or insecure TLS.
- **RPM**: `/etc/yum.repos.d/opf-<session>-<slug>.repo` with `gpgcheck=1`, `repo_gpgcheck=0`, `sslverify=1` and the key imported by `rpm --import`; `/etc/pki/opf/<...>-ca.pem` when a CA is supplied.
- **APK**: `/etc/apk/keys/opf-<session>-<slug>.rsa.pub` and the URL appended to `/etc/apk/repositories`; a custom CA is appended to `/etc/ssl/certs/ca-certificates.crt`.

The WSL fetcher removes its repository files during cleanup; Docker containers are discarded together with the session. The key URL is fetched by the OPF host at prepare time and is not persisted.

### 11.4 Cautions

- Use `--repo-key`/`--repo-key-url` with a key you trust; `insecure_skip_gpg_check` and `insecure_skip_tls_verify` exist for controlled labs only and are logged as warnings.
- An `http://` repository disables transport encryption; GPG signing remains mandatory unless explicitly skipped.
- There is no repository authentication in 0.1.0: URLs with `user:password@` are rejected. Publish the repository on a network reachable by the Docker environment or use a mirror without credentials.
- Repository configuration is session-scoped; nothing is installed or persisted on the target, and SHA-256 verification still applies at all three points.

## 12. SSH: authentication, known_hosts, jump hosts and sudo

### 12.1 Authentication

- Authentication methods are tried in order: private key, then password.
- Keys are read from `--ssh-key` (`~` expanded) and parsed with `ssh.ParsePrivateKey`; passphrase-protected keys are not supported (there is no agent or passphrase prompt).
- Passwords come from `--ssh-password` or `OPF_SSH_PASSWORD`; using a password logs `autenticação SSH por senha habilitada; prefira chave privada`.
- Only one destination connection is opened per session, reused for SFTP, pre-check, host info, checksum verification and installation.

### 12.2 Host key verification

Host-key verification is strict by default and uses `known_hosts` (default `~/.ssh/known_hosts`). A missing file is a fatal error:

```
known_hosts "/home/opf/.ssh/known_hosts" indisponível: stat ...: no such file or directory
```

A mismatch fails the handshake with:

```
host key de <host> não confere com <known_hosts>: knownhosts: key mismatch
```

Register the target key before the first session with `ssh-keyscan -p 22 10.20.0.15 >> ~/.ssh/known_hosts`, then review the fingerprint with `ssh-keygen -lf ~/.ssh/known_hosts`.

The client prefers ed25519, then ECDSA, then RSA-SHA2/RSA host-key algorithms, matching OpenSSH defaults so an existing `known_hosts` keeps working.

`--ssh-insecure-skip-host-key` / GUI **Ignorar verificação de host key** / TUI **Ignorar host key** disables verification (`ssh.InsecureIgnoreHostKey`) and is meant only for disposable labs. It is loud in the logs and in the GUI (red banner).

### 12.3 Jump chain

Jumps are dialed in order, each through the previous connection, and then the destination through the last hop. Rules:

- `--ssh-jump` accepts `[user@]host[:port]`; the user defaults to the destination user and the port to 22. IPv6 addresses must be bracketed: `[2001:db8::10]:2222`.
- `--ssh-jump-auth` is mandatory when jumps exist and must provide one value per jump: `password`, `key` or `key+password`.
- Each hop has independent credentials: `--ssh-jump-key`, `--ssh-jump-password` (or `OPF_SSH_JUMP_PASSWORDS`), `--ssh-jump-known-hosts`.
- Empty jump `known_hosts` inherits the destination file; `--ssh-insecure-skip-host-key` also applies to hops.
- Nested jumps are rejected (`jump N não pode ter jumps aninhados`), and credentials are never inherited from the destination.

Example — two hops, first with a key, second with key + password:

```bash
export OPF_SSH_JUMP_PASSWORDS='["","hop2-password"]'
opf-cli --target ubuntu:22.04 --arch amd64 --pkg nginx \
  --ssh-host 10.20.0.15 --ssh-user opf --ssh-key ~/.ssh/id_ed25519 \
  --ssh-jump ops@bastion.example.com \
  --ssh-jump-auth key \
  --ssh-jump-key ~/.ssh/id_ed25519 \
  --ssh-jump ops@internal-bastion:2222 \
  --ssh-jump-auth key+password \
  --ssh-jump-key ~/.ssh/id_ed25519 \
  --install --sudo
```

Passwords are kept in memory only: they are not written to SQLite, the session JSON, the report or the logs, and are not restored when a session is resumed.

### 12.4 Sudo and privileges

Remote installation executes one of:

| Family | Command (without sudo) |
|:---|:---|
| APT | `cd '<remoteDir>' && { DEBIAN_FRONTEND=noninteractive dpkg -i '<pkg>.deb' ...; status=$?; if [ "$status" -ne 0 ]; then DEBIAN_FRONTEND=noninteractive apt-get install -f -y; fi; }` |
| RPM | `cd '<remoteDir>' && { if command -v dnf ...; elif command -v yum ...; else exit 1; fi; $PM localinstall -y '<pkg>.rpm' ...; }` |
| APK | `cd '<remoteDir>' && apk add --allow-untrusted '<pkg>.apk' ...` |

With sudo the whole script is wrapped as `sudo -S -p '' sh -c '<script>'` and the password is written to the command's stdin followed by a newline. Consequences:

- the password never appears in the remote command line or in process listings;
- a password containing CR, LF or NUL is rejected;
- `sudo` must be installed and the account must be allowed to use it; NOPASSWD is neither required nor assumed;
- the GUI and TUI probe `id -u` before installing: `root` disables sudo, a non-root account without sudo fails early with `usuário remoto não é root; habilite "Usar sudo" e informe a senha da conta`;
- the CLI does not probe; choose `--sudo` based on how you authenticated.

### 12.5 Reconnection and remote directory

The SSH connection is attempted up to three times at the beginning of the pipeline (1 s and 2 s backoff). After the connection is established, a connection loss aborts the session with an error; there is no transparent mid-pipeline reconnection — resume from a safe checkpoint instead.

The remote directory defaults to `/tmp/opf-sessao-<id>` and must be absolute for installation (`diretório remoto deve ser absoluto`). It is created recursively over SFTP, and `SHA256SUMS` plus every artifact are uploaded with mode `0644`.

On cancellation or failure OPF removes the remote directory, but only when it starts with `/tmp/opf-` or `/var/tmp/opf-`; any other path is left untouched by design (`diretório remoto fora da área de cleanup`). A declined installation keeps the artifacts in place so an operator can install them manually.

## 13. 13-stage pipeline, cancellation and resume

### 13.1 Stages

The pipeline names are stored in Portuguese and shown by the GUI/TUI; the table below translates them and lists the exact behavior.

| # | Stage (stored name) | What happens | Skipped when |
|:--:|:---|:---|:---|
| 1 | `abrir sessão` | creates the SQLite session, persists requested packages, records the "sessão iniciada" event | only on a resumed session |
| 2 | `pré-check remoto` | `dpkg-query` / `rpm -q` / `apk info` on the target; already-installed roots are recorded as `ja_instalado` and removed from the download list unless `--force-reinstall` | no SSH |
| 3 | `criar ambiente isolado` | Docker container or WSL environment for the target; validates architecture | all packages already installed |
| 4 | `resolver dependências` | resolver lists the dependency closure | as above |
| 5 | `baixar pacotes` | downloads all artifacts inside the environment | as above |
| 6 | `copiar artefatos para o host` | clears `<out>/sessao-<id>`, copies each artifact, compares the host SHA-256 with the in-environment hash (P1) | as above |
| 7 | `gerar e verificar manifesto` | writes `SHA256SUMS` atomically, re-verifies every entry, records packages (P2) | as above |
| 8 | `enviar via SFTP` | uploads artifacts + manifest (only if not already resumed past this point) | no SSH |
| 9 | `verificar checksums no destino` | `cd '<remoteDir>' && sha256sum -c SHA256SUMS` (P3), marks `p3_verificado`, sets packages to `enviado` | no SSH |
| 10 | `confirmar instalação` | prompts the operator (or `--yes`); refusal sets packages to `enviado_nao_instalado` and skips stage 11 | `--install` absent |
| 11 | `instalar remotamente` | runs the family installer; success sets packages to `instalado` and `instalado_verificado` | `--install` absent or declined |
| 12 | `registrar resultado` | writes the closing event | never |
| 13 | `cleanup` | terminates the container/environment, removes host temporaries, WSL repository files and (on cancellation) the remote directory | never |

Stage timeout defaults to 10 minutes (`StageTimeout`); the finalization/cleanup budget is 2 minutes. The GUI and TUI expose **Timeout de etapa (segundos)**; the CLI always uses the default.

If every root is already installed and `--force-reinstall` is absent, stages 3–11 are recorded as `pulada` and the session completes with `AllAlreadyInstalled` (**Nada a instalar** / **Todos os pacotes já estavam instalados**).

Remote commands are killed through an SSH signal when the context is canceled.

### 13.2 Cancellation

`Ctrl+C` (CLI and TUI) or **Cancelar execução** (GUI) cancels the context. The pipeline:

1. marks the current stage as `falhou` with the cancellation cause;
2. cleans the isolated environment and host temporaries;
3. removes the remote directory when SSH is active and the path is under `/tmp/opf-`/`/var/tmp/opf-`;
4. closes SSH and sets the session status to `abortada`.

An abort does not invalidate artifacts that were already validated, but the session is recorded as `abortada` and any later resume must start from a checkpoint.

### 13.3 Resume by checkpoint

Checkpoint state is stored in `etapas_sessao` (`pendente`, `em_andamento`, `concluida`, `falhou`, `pulada`) and the current stage in `sessoes.etapa_atual`.

Resume is available **only in the GUI and the TUI** (**Continuar**, **Retomar**, `r` in history); the CLI has no resume flag in 0.1.0. The planner picks the safe stage:

| Stored state | Resume from |
|:---|:---|
| stage 12 concluded | 12 (record result) |
| stage 11 failed | 10 (ask for installation again) |
| stage 11 concluded | 12 |
| stage 10 concluded and 11 skipped (installation declined) | 12 |
| stage 10 concluded | 10 |
| stage 9 concluded | 10 |
| stage 8 concluded | 9 |
| stage 7 concluded | 8 |
| stages 3–6 concluded | 3 |
| output directory unknown (empty) | 3 |
| no stage rows recorded for the session | 1 |

When stages 7, 8 or 9 are recorded as concluded, the manifest of the stored output directory is re-verified; if it is missing or corrupted, the planner falls back to stage 3. Resuming at stages 10–11 requires an installer and a new explicit confirmation; resuming at stage 8 or later requires SSH to be configured again.

Resume reuses the stored `config_json`, remote directory, image, WSL distro and environment identifier, but never restores passwords or jump credentials. The GUI/TUI reload the stored values into the screens, clear the detected target and ask for re-authentication before continuing.

## 14. SQLite database

The default database is `./opf.db` (`--db` to change; the GUI/TUI configure it in **Configurações Globais**). The driver is pure Go (`modernc.org/sqlite`), so no CGO is required. On open, OPF enables `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000` and a single connection, then creates or migrates the schema additively.

### 14.1 Tables

`sessoes`:

| Column | Type | Meaning |
|:---|:---|:---|
| `id` | INTEGER PK | session ID |
| `iniciada_em`, `finalizada_em` | DATETIME | RFC 3339 timestamps (UTC) |
| `so_destino`, `versao_destino`, `arch_destino` | TEXT | target |
| `metodo_fetch` | TEXT | `docker` or `wsl` |
| `host_destino` | TEXT | SSH host |
| `include_optional` | INTEGER | 0/1 |
| `status` | TEXT | `em_andamento`, `concluida`, `falhou`, `abortada` |
| `etapa_atual` | INTEGER | 1–13 |
| `output_dir`, `remote_dir` | TEXT | local and remote destinations |
| `imagem_docker`, `wsl_distro`, `ambiente_id` | TEXT | environment identity |
| `config_json` | TEXT | non-secret session configuration |
| `host_info_json` | TEXT | host facts collected over SSH |
| `arquivada` | INTEGER | 0/1 logical archive flag |

`pacotes`:

| Column | Type | Meaning |
|:---|:---|:---|
| `id`, `sessao_id` | INTEGER | identification; `sessao_id` cascades on delete |
| `nome`, `versao`, `arquitetura` | TEXT | package identity |
| `status` | TEXT | `baixado`, `enviado`, `enviado_nao_instalado`, `instalado`, `falhou`, `ja_instalado` |
| `eh_dependencia` | INTEGER | 1 when the package is not a root |
| `caminho_local` | TEXT | artifact path on the host |
| `sha256`, `sha256_p1`, `sha256_p2` | TEXT | final, in-environment and host digests |
| `versao_anterior` | TEXT | previously installed version captured before an upgrade |
| `p3_verificado`, `instalado_verificado` | INTEGER | 0/1 verification flags |

`eventos`: `id`, `sessao_id`, `timestamp`, `tipo` (`download`, `transferencia`, `instalacao`, `erro`, `info`, `precheck`), `mensagem`, `detalhes` (JSON).

`etapas_sessao`: `sessao_id`, `etapa` (1–13), `nome`, `status` (`pendente`, `em_andamento`, `concluida`, `falhou`, `pulada`), `iniciada_em`, `finalizada_em`, `erro`; primary key `(sessao_id, etapa)`.

`pacotes_solicitados`: `id`, `sessao_id`, `nome`, `status` (default `pendente`), `motivo`; keeps the roots across resumes even when the current package list is empty.

Operational notes:

- The database contains package inventories, host paths and host facts. It stores no passwords or private keys, but treat it as sensitive inventory data and back it up (including `-wal` and `-shm`) together with the output directory.
- SQLite serializes writers; a long pipeline keeps one connection open, so external writers may see `database is locked`. `busy_timeout` is 5 s.

### 14.2 Useful queries

```bash
sqlite3 ./opf.db ".headers on" ".mode column"
```

```sql
-- last sessions with package counters
SELECT s.id, s.so_destino || ':' || s.versao_destino AS alvo, s.status,
       s.etapa_atual, s.host_destino,
       COUNT(p.id) AS pacotes,
       SUM(CASE WHEN p.status = 'instalado' THEN 1 ELSE 0 END) AS instalados
FROM sessoes s
LEFT JOIN pacotes p ON p.sessao_id = s.id
GROUP BY s.id
ORDER BY s.id DESC
LIMIT 10;

-- packages of a session, roots first
SELECT nome, versao, arquitetura, status, eh_dependencia, sha256
FROM pacotes
WHERE sessao_id = 12
ORDER BY eh_dependencia, nome;
```

## 15. Reports and Syslog

### 15.1 JSON report

`--export-json <path>` writes a single JSON object:

| Field | Content |
|:---|:---|
| `sessao` | complete session row (target, status, timestamps, paths, fetch method, stage, flags) |
| `host_info` | `host`, `usuario`, `kernel`, `uptime`, `cpu`, `memoria`, `disco` collected over SSH |
| `pacotes_solicitados` | roots requested for the session |
| `pacotes` | packages with an extra `papel` field (`principal` or `dependente`) |
| `eventos` | session events |
| `local_destino`, `remote_destino` | output and remote directories |
| `gerado_em` | report generation timestamp (UTC) |

### 15.2 Markdown report

`--export-markdown <path>` renders the same data as Markdown with the sections `Relatório da Sessão #<id>`, `Host remoto`, `Destino e armazenamento`, `Pacotes solicitados`, `Pacotes` and `Eventos`. Section labels and the closing note (`Exportação gerada localmente. Nenhum segredo é incluído.`) are in Portuguese in 0.1.0.

The GUI report screen shows this Markdown, and both the GUI and the TUI write exports to the configured output directory as `opf-relatorio-<id>.json` / `.md`. Reports are produced from SQLite on demand: the GUI/TUI can render and export any historical session, while the CLI `--export-json` / `--export-markdown` flags export the session it just executed (the CLI has no resume or report-only command in 0.1.0).

### 15.3 External Syslog

When enabled (`--syslog` + `--syslog-host`, or **Habilitar** in the GUI/TUI settings), every event is also queued to an external Syslog endpoint over RFC 5424:

```
<14>1 2026-11-21T03:12:44.512Z opf-host opf - - - {"sessao_id":12,"tipo":"info","mensagem":"sessão iniciada","detalhes":{...}}
```

| Property | Value |
|:---|:---|
| Facility/severity | `user.info` (PRI 14); `user.err` (PRI 11) for `tipo=erro` |
| Transport | UDP (default) or TCP with newline framing |
| Default port | 514 |
| APP-NAME/tag | `opf` (pattern `[A-Za-z0-9][A-Za-z0-9._-]*`) |
| Device hostname | local hostname, or `-` when unavailable |
| Queue | 512 events; overflow is dropped with a warning |
| Retries | 3 attempts, 200 ms exponential backoff, 2 s timeout per attempt |

Delivery is best-effort and asynchronous: SQLite remains the source of truth, and a Syslog outage never blocks or fails the pipeline. Reports can also be sent as a single `relatorio` event from the GUI/TUI report screen; failures are shown as a dialog/label message but do not alter the session.

## 16. Troubleshooting

| Symptom / message | Cause | Resolution |
|:---|:---|:---|
| `known_hosts "..." indisponível` | file does not exist or is not a regular file | create it with `ssh-keyscan -p <port> <host> >> ~/.ssh/known_hosts` and review the fingerprint; or point `--ssh-known-hosts` to the right file |
| `host key de <host> não confere com ...` | the target key changed or known_hosts holds another key | verify the change with the infrastructure team, then update the entry (`ssh-keygen -R <host>`) |
| `abrir TCP para ...` / `handshake SSH ...` | wrong host/port, firewall, SSH not running | test with `ssh -v -p <port> <user>@<host>`; check `--ssh-jump` order if jumps are used |
| `criar contêiner Docker: ...` / `Cannot connect to the Docker daemon` | daemon not running or not reachable by the current user | start Docker Engine/Desktop, check `DOCKER_HOST` and socket permissions |
| `no matching manifest` / `pull access denied` | image tag does not exist for the target/architecture | pass `--image`, or let OPF detect the target so presets are applied |
| `distro WSL "..." não registrada (registradas: ...)` | WSL distribution name differs or is not installed | `wsl -l -v`, then `--wsl-distro <exact name>`; WSL is APT-only |
| `execução WSL não está como root` | the distribution refused `-u root` | enable the distro for the current user (`wsl -d <distro>` once) and retry |
| `pacote indisponível para <os>:<version>` / `No package ... available` | package name differs in the target distribution or the repository is missing | check the target's package name, enable the additional repository, or use `--with-optional` |
| `checksum divergente para "..."` / `cópia corrompida do artefato` | corrupted download or tampered file | delete `./opf-output/sessao-<id>` and rerun; investigate the network/proxy |
| `manifesto vazio` / `artefato ausente` | output directory modified between stages | rerun the session; do not edit `SHA256SUMS` |
| `--yes exige --install` / `--sudo exige --install` | confirmation flags without installation | add `--install` (and `--ssh-host`) |
| `usuário remoto não é root; habilite "Usar sudo"...` (GUI/TUI) | non-root account, sudo unchecked | enable sudo and provide the account password |
| `opf-gui requer CGO_ENABLED=1 ...` | GUI built without CGO | install gcc + graphics libraries and run `make build-gui` |
| `timeout inválido` / `porta Syslog inválida` (GUI/TUI) | settings validation | use a positive integer timeout and a port between 1 and 65535 |
| `syslog indisponível após retries` (warning) | Syslog endpoint unreachable | events remain in SQLite; fix the endpoint or disable Syslog |
| `database is locked` | another process writing to `opf.db` | stop the other process or use a separate `--db` per run |

Diagnostic checklist when a session fails:

1. read the last `erro` event for the session (`eventos` table or report);
2. check the stage that failed in `etapas_sessao`;
3. inspect the log lines from the isolated environment (they contain the failing command and its exit code);
4. confirm the artifact inventory and manifest are intact;
5. if the failure happened after stage 7, resume from stage 3 in the GUI/TUI after fixing the environment.

## 17. Hardening checklist

- Prefer key authentication and protect private keys. OPF cannot prompt for passphrases, so compensate with strict host controls.
- Never pass `--ssh-password` on a shared machine: prefer `OPF_SSH_PASSWORD`/`OPF_SSH_JUMP_PASSWORDS`, and unset them after the run.
- Keep host-key verification enabled; treat `--ssh-insecure-skip-host-key` as a lab-only escape hatch.
- Verify release binaries against `SHA256SUMS` and verify artifacts against the session `SHA256SUMS` before moving them to the target.
- Use a dedicated, least-privileged SSH account. Grant sudo only where installation is required and never assume NOPASSWD.
- Keep the remote directory under `/tmp/opf-*` or `/var/tmp/opf-*` when you want automatic cleanup; a custom path is left untouched.
- Pin additional repositories to HTTPS + GPG and keep the insecure TLS/GPG opt-ins off outside controlled labs.
- Protect `opf.db`, `config.json` and the output directory: they carry inventory data (host, package list, paths) even though no secrets are stored.
- Prefer a local relay for Syslog; UDP carries no encryption or delivery guarantee.
- Run the security suite and scanners in CI (`go test ./test/security/...`, `gitleaks`, `govulncheck`, `gosec`) and review findings before each release.
- Validate new distributions in a lab before production use: preset validation status is advisory, not a guarantee.

## 18. FAQ

**Does the target need internet?** No. It needs SSH/SFTP (when remote operations are used) and a package manager able to install local files. Downloads happen on the OPF host.

**Can I run OPF fully air-gapped?** The OPF host must reach the package repositories (and image registries) once. After the artifacts are on the target, installation uses only local files. If `dpkg` fails and `apt-get install -f` runs, it may try the network; a complete dependency closure avoids that.

**Why are messages in Portuguese?** In 0.1.0 the CLI/TUI/GUI texts and report labels are pt-BR. Documentation is bilingual (EN default, PT-BR linked).

**Can I download for a different architecture?** Yes, set `--arch`. Docker runs the matching platform; the resolver downloads for that architecture.

**Can I install without SSH?** No. Installation requires SSH plus explicit confirmation; without SSH OPF only downloads and verifies.

**Can I resume a CLI session?** Not in 0.1.0. Resume is a GUI/TUI feature; the CLI always starts a new session.

**Are jump-host passwords stored?** No. They live in memory for the run and must be re-entered when resuming.

**Where are the artifacts?** `<out>/sessao-<id>/` (default `./opf-output/sessao-<id>/`), together with `SHA256SUMS`.

**What happens if I decline installation?** The session completes, artifacts stay in the remote directory and packages are recorded as `enviado_nao_instalado`.

## 19. Glossary

| Term | Meaning |
|:---|:---|
| Artifact | A downloaded package file plus metadata and SHA-256. |
| APT / RPM / APK | Package families and their managers/tools: `apt`/`dpkg`, `yum`/`dnf`, `apk`. |
| Checkpoint | Persisted per-stage progress that allows a safe resume. |
| Isolated environment | Container or WSL distribution used to resolve and download packages. |
| Jump host | Intermediate SSH server in the path to the target. |
| known_hosts | OpenSSH-format file with trusted host keys. |
| Manifest (`SHA256SUMS`) | Sorted list of `<sha256>  <filename>` for every artifact. |
| P1 / P2 / P3 | Integrity checkpoints inside the environment, on the host and on the target. |
| Pre-check | Remote query for packages already installed on the target. |
| Root package | A package explicitly requested by the user. |
| Session | One pipeline execution recorded in SQLite. |
| Target | Distribution, version and architecture that artifacts are built for. |
| WSL fetcher | Isolated environment implemented with a registered Windows Subsystem for Linux distribution. |

## 20. Version and license

This manual documents **Offline Package Fetcher 0.1.0**. OPF is distributed under the **GNU General Public License v3.0**; see [LICENSE](../LICENSE) for the full text. The project was developed with heavy AI assistance, as disclosed in the [README](../README.md#ai-assisted-development).

Related documents: [README (EN)](../README.md) · [README (PT-BR)](README.pt-BR.md) · [Manual (PT-BR)](MANUAL.pt-BR.md) · [Security (EN)](SECURITY.md) · [Security (PT-BR)](SECURITY.pt-BR.md) · [Technical document (PT-BR)](project-description.pt-BR.md) · [GUI/TUI specification (PT-BR)](specs/gui-tui.pt-BR.md) · [Changelog](../CHANGELOG.md)
