# Changelog

All notable changes to **Offline Package Fetcher (OPF)** are documented in this
file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
