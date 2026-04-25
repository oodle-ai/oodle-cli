VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: generate build test test-integration lint clean

generate:
	go generate ./internal/client/...

# NOTE: the -X ldflags below are kept in sync with `.goreleaser.yaml`.
# If you add or change an -X flag, update both files.
build:
	go build -ldflags "-X github.com/oodle-ai/oodle-cli/internal/cmd.version=$(VERSION) -X github.com/oodle-ai/oodle-cli/internal/cmd.commit=$(COMMIT) -X github.com/oodle-ai/oodle-cli/internal/cmd.date=$(DATE)" -o bin/oodle ./cmd/oodle

test:
	go test ./...

test-integration:
	go test -tags integration -v -count=1 ./test/...

lint:
	golangci-lint run

clean:
	rm -rf bin/
