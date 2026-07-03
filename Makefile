# ai-playbook — local build/test gate.
# `make check` is the pre-commit gate: it runs build, vet, lint, fmt-check and
# test, and fails on ANY lint finding or unformatted file (mirrors CI).

GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

VERSION ?= dev
LDFLAGS := -X github.com/Townk/ai-playbook/internal/cli.Version=$(VERSION)

.PHONY: build build-release vet test lint fmt-check check docs docs-check

build:
	go build ./...

# build-release stamps both binaries with VERSION (defaults to "dev";
# override with `make build-release VERSION=1.2.3`), mirroring what
# goreleaser does for release/snapshot builds.
build-release:
	go build -ldflags '$(LDFLAGS)' -o bin/ai-playbook ./cmd/ai-playbook
	go build -ldflags '$(LDFLAGS)' -o bin/apb ./cmd/apb

docs:
	go run ./cmd/docgen

# docs-check verifies docs/man/*.1 and completions/_ai-playbook are up to date
# with the climeta registry they're generated from (docgen is idempotent, so a
# clean re-run must produce no diff). Catches registry edits that forgot `make
# docs`.
docs-check:
	go run ./cmd/docgen
	git diff --exit-code docs/man completions

vet:
	go vet ./...

test:
	go test ./...

lint:
	$(GOLANGCI) run

fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofmt needs to run on:"; echo "$$files"; \
		exit 1; \
	fi

check: build vet lint fmt-check test docs-check
