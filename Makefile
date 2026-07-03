# ai-playbook — local build/test gate.
# `make check` is the pre-commit gate: it runs build, vet, lint, fmt-check and
# test, and fails on ANY lint finding or unformatted file (mirrors CI).

GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

.PHONY: build vet test lint fmt-check check docs

build:
	go build ./...

docs:
	go run ./cmd/docgen

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

check: build vet lint fmt-check test
