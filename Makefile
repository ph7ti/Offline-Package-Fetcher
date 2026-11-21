GO ?= go
BINARY ?= bin/opf-cli

# Production build metadata:
#   -trimpath   remove absolute build-host paths from the binary
#   -s -w       strip the symbol table and DWARF debug data
#   -buildid=   drop the Go build ID (a hash of the build inputs)
# The three linker flags are what CI and the release workflow use, so local
# binaries match the published artifacts byte-for-byte in this respect.
LDFLAGS ?= -s -w -buildid=

# UPX is optional locally but mandatory for Linux in CI/release. `--best
# --lzma` is the strongest setting and is what the workflows use. Windows .exe
# files stay raw (Cortex XDR blocks UPX-packed PE) and macOS stays untouched
# (UPX cannot pack Mach-O); the release ships Windows inside .zip archives.
# Override the path with `make compress UPX=/path/to/upx` if UPX is not on PATH.
UPX ?= upx
UPX_FLAGS ?= --best --lzma

.PHONY: build build-linux build-gui build-tui test test-integration docker-test-image compress package-windows clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/opf-cli

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/opf-cli-linux-amd64 ./cmd/opf-cli

build-gui:
	mkdir -p bin
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/opf-gui ./cmd/opf-gui

build-tui:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/opf-tui ./cmd/opf-tui

# Compress the Linux binaries in place and verify each packed file. Windows
# .exe files must stay raw (Cortex XDR blocks packed PE) and macOS cannot be
# packed by UPX; the release publishes Windows as .zip archives instead.
# Run after `make build-linux` (and, on Linux, `make build build-tui build-gui`).
compress:
	@if ! command -v "$(UPX)" >/dev/null 2>&1; then \
		echo "error: UPX not found (looked for '$(UPX)')." >&2; \
		echo "       Install UPX from https://upx.github.io or run 'make compress UPX=/path/to/upx'." >&2; \
		exit 1; \
	fi
	@set -e; found=0; \
	binaries="bin/opf-cli-linux-amd64 bin/opf-cli bin/opf-tui bin/opf-gui"; \
	for f in $$binaries; do \
		if [ -f "$$f" ]; then \
			echo "==> $(UPX) $(UPX_FLAGS) $$f"; \
			$(UPX) $(UPX_FLAGS) "$$f"; \
			$(UPX) -t "$$f"; \
			found=1; \
		fi; \
	done; \
	if [ "$$found" -eq 0 ]; then \
		echo "error: no Linux build artifacts found in bin/; run 'make build-linux' first." >&2; \
		exit 1; \
	fi; \
	echo "OK: every UPX-packed Linux binary passed 'upx -t'."

# Package the raw Windows executables in bin/ as .zip archives, matching the
# release workflow (the .exe itself is never UPX-packed).
package-windows:
	@if ! command -v zip >/dev/null 2>&1; then \
		echo "error: zip not found." >&2; \
		exit 1; \
	fi
	@set -e; found=0; \
	for f in bin/*.exe; do \
		[ -f "$$f" ] || continue; \
		echo "==> zip -9 -j $$f.zip $$f"; \
		zip -9 -j "$$f.zip" "$$f"; \
		found=1; \
	done; \
	if [ "$$found" -eq 0 ]; then \
		echo "error: no bin/*.exe found; run a Windows build first." >&2; \
		exit 1; \
	fi

# Tests pin the interface language: i18n follows the OS language by default,
# while the assertions use Portuguese source strings. OPF_LANG keeps the suite
# deterministic on any runner or developer machine.
test:
	OPF_LANG=pt-BR CGO_ENABLED=0 $(GO) test ./...

test-integration:
	OPF_LANG=pt-BR CGO_ENABLED=0 $(GO) test -tags=integration -count=1 -timeout=30m ./...

docker-test-image:
	docker build -f docker/ubuntu-22.04.Dockerfile -t opf/ubuntu-22.04:f0 docker

clean:
	rm -rf bin
