# ZeoKeel — build, vet and test with the offline Go toolchain.
#
# The project is offline-first: dependencies are pinned in go.mod/go.sum and
# resolved from the local module cache. These are the environment variables the
# README and the release scripts assume:
#
#   GOPROXY=https://goproxy.cn,direct   domestic Go module mirror (China)
#   GOSUMDB=sum.golang.org             checksum database, proxied by goproxy.cn
#   GOFLAGS=-mod=mod                   let the go command maintain go.mod/go.sum
#
# Fully offline builds: `make GOPROXY=off GOSUMDB=off ...` with a warm cache.
#
# Override the toolchain with `make GO=/path/to/go ...`. `make run` starts the
# desktop product (needs a desktop session); `make run ARGS=--headless` or
# `make run-headless` starts the headless product.

GO ?= go
export PATH := /usr/local/go/bin:$(PATH)
GOPROXY ?= https://goproxy.cn,direct
GOSUMDB ?= sum.golang.org
export GOPROXY
export GOSUMDB
export GOFLAGS := -mod=mod

# Extra arguments for `make run` (e.g. ARGS=--headless --data-dir /tmp/zk).
ARGS ?=

.PHONY: all build vet test ci run run-headless clean verify release package icons design-check design-baseline

all: build

# Compile every package, including cmd/zeokeel.
build:
	$(GO) build ./...

# Static checks (no build output is written to the repository).
vet:
	$(GO) vet ./...

# Unit tests, uncached. Everything is headless-safe: the headless product and
# the embedded assemblies are exercised without a display environment.
test:
	$(GO) test ./... -count=1

# Design contract (docs/DESIGN-LINT.md): DS-01/03/06/07/10 AST rules via the
# designlint command, then the Phase 0 grep probes. Stock violations are
# grandfathered in .design-baseline.json; new ones fail.
design-check:
	$(GO) run ./internal/devtools/designlint --strict --baseline .design-baseline.json ./...
	./scripts/design-check.sh
	$(GO) tool mygo vet .

# Refresh the stock ledger after a migration has removed violations. The
# baseline may only shrink in normal PRs (DESIGN-LINT §4.2); this target exists
# for the design-system owner's cleanup commits.
design-baseline:
	$(GO) run ./internal/devtools/designlint --write-baseline ./...
	@echo "updated .design-baseline.json (only remove stale entries in normal PRs)"

# What CI runs.
ci: build vet test

# One-command reproducible verification: gofmt/build/vet/test/-race/regression/
# UI snapshot/three-product smokes (scripts/verify.sh --quick for the fast loop).
verify:
	./scripts/verify.sh $(ARGS)

# Run the desktop product from source (needs a desktop session).
run:
	$(GO) run ./cmd/zeokeel $(ARGS)

# Run the headless product from source (no display environment required).
run-headless:
	$(GO) run ./cmd/zeokeel --headless $(ARGS)

clean:
	$(GO) clean ./...

# Framework-native packaging, the same `go tool mygo build` CI runs: Windows
# GUI executable + NSIS installer under build/windows-amd64/, Linux app
# directory under build/linux-amd64/ (executable, .desktop, icon, .deb,
# .tar.gz, PKGBUILD) via mygo.json. Needs rsvg-convert and NSIS (makensis).
# The AppImage step is scripts/build-appimage.sh, called by the release
# workflow after this build. Usage: make package
package:
	./scripts/build-app.sh -platform linux/amd64,windows/amd64

# Regenerate every raster icon from the two logo SVGs (scripts/gen-icons.sh).
icons:
	./scripts/gen-icons.sh

# Plain dist/ binary build (the pre-framework release path; CI/release.yml now
# packages with `make package`/mygo build).
# Usage: make release VERSION=0.1.0
VERSION ?= $(shell cat VERSION 2>/dev/null || echo 0.1.0)
release:
	$(GO) build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= \
			-X github.com/zeokeel/zeokeel/internal/host.Version=v$(VERSION) \
			-X github.com/zeokeel/zeokeel/internal/host.Commit=$$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown) \
			-X github.com/zeokeel/zeokeel/internal/host.BuildTime=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o dist/zeokeel ./cmd/zeokeel
	@echo "built dist/zeokeel v$(VERSION)"
