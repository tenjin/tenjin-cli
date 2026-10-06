.PHONY: build test fmt-check lint tidy generate release-check release-snapshot

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILDINFO := github.com/tenjin/tenjin-cli/internal/buildinfo
LDFLAGS   := -s -w -X $(BUILDINFO).version=$(VERSION) -X $(BUILDINFO).commit=$(COMMIT) -X $(BUILDINFO).date=$(DATE)

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/tenjin ./cmd/tenjin

test:
	go test -race ./...

generate:
	go generate ./...

tidy:
	go mod tidy

# Covers tracked and not-yet-tracked Go files, so it works before the first commit.
fmt-check:
	@unformatted="$$(git ls-files --cached --others --exclude-standard '*.go' | xargs gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Keep the version in sync with golangci-lint-action's `version` in
# .github/workflows/linter.yaml so local and CI enforce the same rules.
lint: fmt-check
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 run

GORELEASER := go run github.com/goreleaser/goreleaser/v2@v2.18.2

# Validates .goreleaser.yaml.
release-check:
	$(GORELEASER) check

# Builds every release archive into dist/ as a real release would, and
# publishes nothing.
release-snapshot:
	$(GORELEASER) release --snapshot --clean
