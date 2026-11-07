# ZeoKeel — build, vet and test with the offline Go toolchain.
#
# The project is offline-first: dependencies are pinned in go.mod/go.sum and
# resolved from the local module cache. These are the environment variables the
# README and the release scripts assume:
#
#   GOPROXY=off        never reach the network; fail instead of hanging
#   GOSUMDB=off        no checksum-database lookups (the module cache is trusted)
#   GOFLAGS=-mod=mod   let the go command maintain go.mod/go.sum offline
#
# Override the toolchain with `make GO=/path/to/go ...`. `make run` starts the
# desktop product (needs a desktop session); `make run ARGS=--headless` or
# `make run-headless` starts the headless product.

GO ?= go
export PATH := /usr/local/go/bin:$(PATH)
export GOPROXY := off
export GOSUMDB := off
export GOFLAGS := -mod=mod

# Extra arguments for `make run` (e.g. ARGS=--headless --data-dir /tmp/zk).
ARGS ?=

.PHONY: all build vet test ci run run-headless clean verify

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
