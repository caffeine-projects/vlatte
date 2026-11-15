# Static responses; filters and authentication are not simulated. The adoption scenario plays an order of Buddy:
# POST /orders holds the pet, so it reads PENDING and a second order is refused, until DELETE /orders/{id} cancels it.
.PHONY: example\:petstore
example\:petstore: ## run the Pet Store mock API on 127.0.0.1:8080
	@go run ./cmd/vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081 --workdir=.local/petstore --mock-files='examples/01-petstore/testdata/*.mock.yaml'

LAB_ADDR ?= 127.0.0.1:8080

.PHONY: example\:private-network
example\:private-network: ## run the private-network lab on LAB_ADDR (default 127.0.0.1:8080)
	@go run ./cmd/vlatte --addr="$(LAB_ADDR)" --admin-addr=127.0.0.1:8081 --workdir=.local/private-network --mock-files='examples/04-private-network/testdata/private-network.mock.yaml'

.PHONY: example\:private-network-test
example\:private-network-test: ## run the NetBird encrypted Login test (separate module)
	@go -C examples/04-private-network/management test -race -timeout 1m ./...

# Every Go module under examples/: none of them is part of the vlatte module.
EXAMPLE_MODULES = $(patsubst %/go.mod,%,$(shell find examples -name go.mod -not -path '*/node_modules/*'))

.PHONY: example\:check
example\:check: ## check that every example module is tidy, vets clean and passes its tests
	@for mod in $(EXAMPLE_MODULES); do \
		go -C "$$mod" mod tidy -diff && \
		go -C "$$mod" vet ./... && \
		go -C "$$mod" test -race -timeout 1m ./... || exit 1; \
	done
