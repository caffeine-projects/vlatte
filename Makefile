SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

# allow user specific optional overrides
-include Makefile.overrides

include examples/Makefile

GOLANGCI_LINT_VERSION ?= v2.14.0 # keep in sync with the golangci-lint-action version in .github/workflows/ci.yml
GORELEASER_VERSION ?= v2.20.0 # keep in sync with the goreleaser-action version in .github/workflows/release.yml and ci.yml
GOVULNCHECK_VERSION ?= v1.9.0 # keep in sync with the govulncheck commit in .github/workflows/security.yml
FUZZTIME ?= 30s # time per target for make fuzz

export GORELEASER_VERSION GOLANGCI_LINT_VERSION GOVULNCHECK_VERSION FUZZTIME

.PHONY: test
test: ## run tests, the examples included
	@go test -v -race -timeout 1m ./...
	@$(MAKE) --no-print-directory example:check

.PHONY: testfail
testfail: ## run tests and print the name of failed ones
	@log=$$(mktemp) && st=$$(mktemp) || exit 1; \
	{ go test -timeout 60000ms -race -v ./... 2>&1; echo $$? >"$$st"; } | tee "$$log"; \
	status=$$(cat "$$st"); \
	grep -E '^[[:space:]]*--- FAIL: ' "$$log" | sed -E 's/^[[:space:]]*--- FAIL: ([^ ]+).*/\1/'; \
	rm -f "$$log" "$$st"; exit $$status

# Generated test protobuf is already excluded by Codecov and SonarCloud. Do not instrument it: its entries make
# SonarCloud warn once per package profile, even though it correctly ignores that source.
.PHONY: testci
testci: ## run tests with a focus on ci
	@set -eu; \
	coverage_packages=$$(go list ./...); \
	coverage_packages=$$(printf '%s\n' "$$coverage_packages" | awk '!/\/mockgrpc\/internal\/protobuf(\/|$$)/' | paste -sd, -); \
	go test -v -race -timeout 10m ./... -coverpkg="$$coverage_packages" -coverprofile=coverage.txt

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

# The gate every change passes before it is done. Not covered: the Docker build and its smoke test (make docker),
# the GoReleaser config (make release-check), the vulnerability scans (make vuln), the second Go version CI tests,
# coverage and SonarCloud. Tests run without -v and without coverage, unlike make testci.
.PHONY: check
check: ## run the quality gate CI runs: format, modules, lint, tests, examples, docs links
	@fail=0; \
	step() { \
		name="$$1"; shift; \
		if out="$$("$$@" 2>&1)"; then echo "ok    $$name"; else echo "FAIL  $$name"; echo "$$out"; fail=1; fi; \
	}; \
	step format     $(MAKE) --no-print-directory check-fmt; \
	step modules    go mod tidy -diff; \
	step lint       $(MAKE) --no-print-directory lint; \
	step test       go test -race -timeout 1m ./...; \
	step examples   $(MAKE) --no-print-directory example:check; \
	step docs-build $(MAKE) --no-print-directory docs-build; \
	step docs-links $(MAKE) --no-print-directory docs-check-links; \
	exit $$fail

.PHONY: check-fmt
check-fmt: ## fail when a file is not gofumpt-formatted
	@out="$$(go tool gofumpt -l .)" || exit 1; [ -z "$$out" ] || { echo "$$out"; echo "run 'make fmt'"; exit 1; }

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
	@docker build --build-arg VERSION="$$(git describe --tags --always --dirty)" -t vlatte:dev .

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
tidy: ## tidy go.mod and go.sum, the example modules included
	@go mod tidy
	@for mod in $(EXAMPLE_MODULES); do go -C "$$mod" mod tidy || exit 1; done

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

.PHONY: docs-version
docs-version: ## update the documented stable release (VERSION=v1.1.0)
	@node docs/scripts/version.mjs "$(VERSION)"

.PHONY: docs-build
docs-build: ## build the docs site into docs/.vitepress/dist
	@cd docs && npm ci --ignore-scripts && npm run build

.PHONY: docs-check-links
docs-check-links: ## check internal links after make docs-build (requires lychee)
	@set -eu
	lychee --offline --root-dir "$(CURDIR)/docs/.vitepress/dist" \
		--remap '^https://vlatte\.dev/ file://$(CURDIR)/docs/.vitepress/dist/' \
		--extensions html docs/.vitepress/dist
	git ls-files --cached --others --exclude-standard -- '*.md' | \
		lychee --offline --root-dir "$(CURDIR)/docs/.vitepress/dist" \
			--remap '^https://vlatte\.dev/ file://$(CURDIR)/docs/.vitepress/dist/' --files-from -

.PHONY: docs-check-external-links
docs-check-external-links: ## check external links after make docs-build (requires lychee)
	@set -eu
	git ls-files --cached --others --exclude-standard -- '*.md' | \
		lychee --scheme http --scheme https --include-fragments=none \
			--exclude '^https://vlatte\.dev/' --root-dir "$(CURDIR)/docs/.vitepress/dist" \
			--files-from - 'docs/.vitepress/dist/**/*.html'

# --
# Miscellaneous
# --

.PHONY: help
help: ## show help
	@awk 'BEGIN {FS = ": ## "} /^[a-zA-Z0-9_\\:-]+: ## / {gsub(/\\:/, ":", $$1); printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
