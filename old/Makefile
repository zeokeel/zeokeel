GO ?= go
export CGO_ENABLED := 0

.PHONY: build build-all build-check windows package-windows package-linux package-macos release icon test vet fmt check nocgo clean-check previews visual-check run clean version

# Every build product lands in dist/ with a stable per-platform name; see
# scripts/build.sh. Nothing is written to the repository root.
build:
	sh scripts/build.sh native

build-all:
	sh scripts/build.sh all

# Compile check only (writes nothing).
build-check:
	$(GO) build ./...

windows:
	sh scripts/build.sh windows

package-windows:
	sh scripts/build.sh package-windows

package-linux:
	sh scripts/build.sh package-linux

package-macos:
	sh scripts/build.sh package-macos

# Build every release artifact locally (same outputs as the release CI workflow).
release:
	sh scripts/release.sh

# Print the version/commit/build time the next build would inject.
version:
	@sh -c 'echo "version:    $${ZEOKEEL_VERSION:-$$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)}"; \
	         echo "commit:     $${ZEOKEEL_COMMIT:-$$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"'

# Regenerate the committed Windows icon resource (needs MinGW windres).
icon:
	sh scripts/gen-windows-resource.sh

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l ./cmd ./internal

check: fmt vet test
	@test -z "$$(gofmt -l ./cmd ./internal)" || (echo "gofmt needed"; exit 1)
	sh scripts/check-nocgo.sh

nocgo:
	sh scripts/check-nocgo.sh

# Verify the committed tree (not the working tree) builds without ignored files.
clean-check:
	sh scripts/check-clean-checkout.sh

# Regenerate the visual goldens in docs/design/preview/ (opt-in; review the diff
# before committing the PNGs). See docs/design/preview/README.md.
previews:
	ZEOKEEL_UPDATE_PREVIEWS=1 $(GO) test -count=1 -run 'TestRender.*NativePreview' ./internal/shell

# Same-machine visual regression against the committed goldens (opt-in).
visual-check:
	ZEOKEEL_VISUAL=1 $(GO) test -count=1 -run TestVisualRegression ./internal/shell

run:
	$(GO) run ./cmd/zeokeel

# Removes dist/ and the legacy repo-root binaries (*.test too); keeps .cache/,
# .probe/ and .evolve/ because those are local state, not build artifacts.
clean:
	sh scripts/build.sh clean
