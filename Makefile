GO ?= go
BINARY := bin/codeforge-mcp
CGO_ENABLED ?= 0

.PHONY: all build test test-race test-cover fmt fmt-check vet check verify run clean
all: check build

build:
	mkdir -p bin
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags='-s -w' -o $(BINARY) ./cmd/codeforge-mcp

test:
	$(GO) test -count=1 ./...

test-race:
	$(GO) test -race -count=1 ./...

test-cover:
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@files="$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$files" ]; then echo "unformatted Go files:"; echo "$$files"; exit 1; fi

vet:
	$(GO) vet ./...

check: fmt-check test vet

verify: check test-race test-cover build

run:
	$(GO) run ./cmd/codeforge-mcp

clean:
	rm -rf bin coverage.out
