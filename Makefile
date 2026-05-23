.PHONY: build run test clean release-dryrun release-snapshot lint help

BINARY := observo

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

build: ## Build the binary into ./observo
	go build -o $(BINARY) .

run: build ## Build + run --version
	./$(BINARY) --version

test: ## Run go tests
	go test -race -count=1 ./...

clean: ## Remove build artifacts
	rm -f $(BINARY) $(BINARY).exe
	rm -rf dist/

release-snapshot: ## GoReleaser snapshot — does NOT publish (no tag needed)
	goreleaser release --snapshot --clean --skip=publish

release-dryrun: ## GoReleaser dry-run that DOES sign/upload to staging (CI-only normally)
	goreleaser release --skip=publish --clean

lint: ## Run go vet (golangci-lint optional, install separately)
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "(skipped golangci-lint — not installed)"
