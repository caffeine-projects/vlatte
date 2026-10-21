.ONESHELL:
.DEFAULT_GOAL := help

# allow user specific optional overrides
-include Makefile.overrides

# keep in sync with the golangci-lint-action version in .github/workflows/ci.yml
GOLANGCI_LINT_VERSION ?= v2.14.0

# time per target for make fuzz
FUZZTIME ?= 30s

export

.PHONY: test
test: ## run tests
	@go test -v -race -timeout 1m ./...

.PHONY: testfail
testfail: ## run tests and print the name of failed ones
	@log=$$(mktemp) && st=$$(mktemp) || exit 1; \
	{ go test -timeout 60000ms -race -v ./... 2>&1; echo $$? >"$$st"; } | tee "$$log"; \
	status=$$(cat "$$st"); \
	grep -E '^[[:space:]]*--- FAIL: ' "$$log" | sed -E 's/^[[:space:]]*--- FAIL: ([^ ]+).*/\1/'; \
	rm -f "$$log" "$$st"; exit $$status

.PHONY: testci
testci: ## run tests with a focus on ci
	@go test -v -race -timeout 10m ./... -coverpkg=./... -coverprofile=coverage.txt

.PHONY: fuzz
fuzz: ## run every fuzz target for FUZZTIME each
	@set -e; \
	for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' $$pkg | grep '^Fuzz' || true); do \
			echo "fuzz $$pkg $$target"; \
			go test -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZTIME) $$pkg; \
		done; \
	done

.PHONY: lint
lint: ## run linters
	@GOTOOLCHAIN=go$$(go list -m -f '{{.GoVersion}}') go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

.PHONY: bench
bench: ## run benchmarks
	@go test -v ./... -bench=. -count 2 -benchmem -run=^#

.PHONY: cov
cov: ## run tests and generate coverage report
	@set -e; mkdir -p ./out
	@go test -v -race -coverpkg=./... -coverprofile=out/coverage.out ./...
	@go tool cover -html=out/coverage.out -o out/coverage.html

.PHONY: build
build: ## build the vlatte CLI into bin/
	@go build -o bin/vlatte ./cmd/vlatte

.PHONY: deps
deps: ## dependencies
	@go mod download

.PHONY: tidy
tidy: ## tidy go.mod and go.sum
	@go mod tidy

.PHONY: fmt
fmt: ## format code with gci and gofumpt
	@GOTOOLCHAIN=go$$(go list -m -f '{{.GoVersion}}') go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) fmt
	@go tool gofumpt -w .

.PHONY: fix
fix: ## apply go fix modernizers
	@go fix ./...

.PHONY: vet
vet: ## run go vet
	@go vet ./...

.PHONY: generate
generate: ## run code generators
	@go generate ./...

.PHONY: vuln
vuln: ## check for known vulnerabilities
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# --
# Miscellaneous
# --

.PHONY: help
help: ## show help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
