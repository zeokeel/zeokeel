GO ?= go
export CGO_ENABLED := 0

.PHONY: build build-all build-check windows package-windows test vet fmt check nocgo run clean

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

run:
	$(GO) run ./cmd/zeokeel

# Removes dist/ and the legacy repo-root binaries (*.test too); keeps .cache/,
# .probe/ and .evolve/ because those are local state, not build artifacts.
clean:
	sh scripts/build.sh clean
