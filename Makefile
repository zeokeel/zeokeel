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

.PHONY: all build vet test ci run run-headless clean verify release package icons web-client web-client-if-available web-client-fresh

all: build

# Compile every package, including cmd/zeokeel. The embedded browser client is
# refreshed first when web/dist exists; a Go-only checkout still builds (the
# gateway then serves a "client bundle not built" page and logs that it has none).
build: web-client-if-available
	$(GO) build ./internal/... ./cmd/...

# Copy the built browser client (web/dist) into the gateway's embed directory.
# See scripts/build-web-client.sh for why a copy is involved (go:embed cannot
# leave its package) and for the BUNDLE_INFO fingerprint.
web-client:
	./scripts/build-web-client.sh

# Inner-loop variant: no npm needed, warns when web/dist is absent.
web-client-if-available:
	./scripts/build-web-client.sh --if-available

# Release guard: refuse to ship a binary whose embedded client is older than the
# client sources. Compares against web/src, web/index.html and web/package.json.
# Acceptance fixture: the real gateway plus a deterministic OpenAI-compatible
# model, so the end-to-end flow runs without a provider key.
.PHONY: acceptance acceptance-tool acceptance-check acceptance-approval acceptance-slow acceptance-update
acceptance:
	bash scripts/acceptance/serve-with-fake-model.sh

acceptance-tool:
	bash scripts/acceptance/serve-with-fake-model.sh --with-tool

# One-shot protocol-level end-to-end (no browser): starts the fixture, drives it
# with internal/devtools/gwprobe, exits with its verdict.
acceptance-check:
	bash scripts/acceptance/serve-with-fake-model.sh --plain --probe plain

acceptance-approval:
	bash scripts/acceptance/serve-with-fake-model.sh --with-tool --probe approval

acceptance-slow:
	bash scripts/acceptance/serve-with-fake-model.sh --slow --probe slow

# The update surface (TRANSPORT §5.1.12) against a real gateway. Reachability of
# GitHub decides available/error; the probe asserts the wiring, so it is safe to
# run offline too.
acceptance-update:
	bash scripts/acceptance/serve-with-fake-model.sh --plain --probe update

web-client-fresh: web-client
	@newer=""; \
	for path in web/src web/index.html web/package.json; do \
		[ -e "$$path" ] || continue; \
		found=$$(find "$$path" -newer internal/gateway/webclient/dist/index.html -print -quit 2>/dev/null); \
		[ -n "$$found" ] && newer="$$found" && break; \
	done; \
	if [ -n "$$newer" ]; then \
		echo "web client is stale: $$newer is newer than the embedded bundle" >&2; \
		echo "rebuild it: (cd web && npm run build) && make web-client" >&2; \
		exit 1; \
	fi

# Static checks (no build output is written to the repository).
vet:
	$(GO) vet ./internal/... ./cmd/...

# Unit tests, uncached. Everything is headless-safe: the headless product and
# the embedded assemblies are exercised without a display environment.
test:
	$(GO) test ./internal/... ./cmd/... -count=1

# The fast local subset of CI (build+vet+test). The hosted CI additionally runs
# gofmt, the protocol/implementation consistency test (docs/TRANSPORT.md §5.1 vs
# the gateway dispatch), the two-tier import-boundary assertions, a targeted
# -race pass, product smokes, cross-compile and installer packaging
# (.github/workflows/ci.yml); full local parity: make verify.
ci: build vet test

# One-command reproducible verification: gofmt/build/vet/protocol-consistency/
# test/-race/two-tier boundaries/headless+ACP smokes (scripts/verify.sh --quick
# for the fast loop).
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
package: web-client-fresh
	./scripts/build-app.sh -platform linux/amd64,windows/amd64

# Regenerate every raster icon from the two logo SVGs (scripts/gen-icons.sh).
icons:
	./scripts/gen-icons.sh

# Plain dist/ binary build (the pre-framework release path; CI/release.yml now
# packages with `make package`/mygo build).
# Usage: make release VERSION=0.1.0
VERSION ?= $(shell cat VERSION 2>/dev/null || echo 0.1.0)
release: web-client-fresh
	$(GO) build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= \
			-X github.com/zeokeel/zeokeel/internal/host.Version=v$(VERSION) \
			-X github.com/zeokeel/zeokeel/internal/host.Commit=$$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown) \
			-X github.com/zeokeel/zeokeel/internal/host.BuildTime=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o dist/zeokeel ./cmd/zeokeel
	@echo "built dist/zeokeel v$(VERSION)"
