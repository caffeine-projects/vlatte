.ONESHELL:
.DEFAULT_GOAL := help

# allow user specific optional overrides
-include Makefile.overrides

include examples/Makefile

# keep in sync with the golangci-lint-action version in .github/workflows/ci.yml
GOLANGCI_LINT_VERSION ?= v2.14.0

# keep in sync with the goreleaser-action version in .github/workflows/release.yml
GORELEASER_VERSION ?= v2.18.3

# keep in sync with the govulncheck commit in .github/workflows/security.yml
GOVULNCHECK_VERSION ?= v1.9.0

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
	@golangci-lint run

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

.PHONY: docker
docker: ## build the vlatte image as vlatte:dev
	@docker build -t vlatte:dev .

.PHONY: release-check
release-check: ## check the GoReleaser config
	@go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

.PHONY: snapshot
snapshot: ## build the release archives into dist/, without signing or publishing them
	@go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean --skip=sign,sbom

.PHONY: deps
deps: ## dependencies
	@go mod download

.PHONY: tidy
tidy: ## tidy go.mod and go.sum
	@go mod tidy

.PHONY: fmt
fmt: ## format code with gci and gofumpt
	@golangci-lint fmt
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
	@go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: docs-dev
docs-dev: ## serve the docs site with live reload
	@cd docs && npm ci --ignore-scripts && npm run dev

.PHONY: docs-build
docs-build: ## build the docs site into docs/.vitepress/dist
	@cd docs && npm ci --ignore-scripts && npm run build

# --
# Miscellaneous
# --

.PHONY: help
help: ## show help
	@awk 'BEGIN {FS = ": ## "} /^[a-zA-Z0-9_\\:-]+: ## / {gsub(/\\:/, ":", $$1); printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
