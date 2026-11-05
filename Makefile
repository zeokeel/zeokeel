GO ?= go
export CGO_ENABLED := 0

.PHONY: build test vet fmt check nocgo run clean

build:
	$(GO) build ./...

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

clean:
	rm -rf .cache build dist
