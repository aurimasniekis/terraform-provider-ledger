# Provider developer entry points. Run `make help` for the list.
GO       ?= go
GOLANGCI ?= golangci-lint
BIN_DIR  ?= bin
BINARY   := $(BIN_DIR)/terraform-provider-ledger
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

.PHONY: help fmt fmt-check vet lint test build docs docs-check workflow-policy check clean

help: ## list the targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-14s %s\n", $$1, $$2}'

fmt: ## rewrite Go files with gofmt -s
	gofmt -s -w .

fmt-check: ## fail when any Go file is not gofmt -s clean
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "not gofmt -s clean:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint with .golangci.yml
	$(GOLANGCI) run ./...

test: ## offline unit tests with the race detector (acceptance tests need TF_ACC=1 and are not run here)
	$(GO) test -race ./...

build: ## build the provider binary, pure Go (CGO_ENABLED=0)
	CGO_ENABLED=0 $(GO) build ./...
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .
	@$(GO) version -m $(BINARY) | grep -q 'CGO_ENABLED=0' || { echo "$(BINARY) was not built with CGO_ENABLED=0"; exit 1; }

docs: ## regenerate docs/ with tfplugindocs; downloads the pinned Terraform CLI (network needed when not cached)
	$(GO) generate ./...

docs-check: docs ## regenerate and validate docs, fail when docs/ differs from what is committed
	$(GO) tool tfplugindocs validate --provider-name ledger --tf-version 1.11.4 # same pin as main.go (ADR-0001 section 10)
	@if [ -n "$$(git status --porcelain -- docs ':!docs/decisions')" ]; then echo "docs/ is out of date; run 'make docs' and commit the result:"; git status --short -- docs ':!docs/decisions'; git diff -- docs ':!docs/decisions'; exit 1; fi

workflow-policy: ## workflow permission policy: the negative fixtures must be rejected, then .github/workflows must pass
	sh scripts/check-workflow-permissions.sh --self-test
	sh scripts/check-workflow-permissions.sh .github/workflows

check: fmt-check vet lint test build docs-check workflow-policy ## everything the checks need, in order

clean: ## remove build output
	rm -rf $(BIN_DIR)
