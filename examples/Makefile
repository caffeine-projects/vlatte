# Static responses; filters and authentication are not simulated. The adoption scenario plays an order of Buddy:
# POST /orders holds the pet, so it reads PENDING and a second order is refused, until DELETE /orders/{id} cancels it.
.PHONY: example\:petstore
example\:petstore: ## run the Pet Store mock API on 127.0.0.1:8080
	@go run ./cmd/vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081 --workdir=.local/petstore --mock-files='examples/01-petstore/testdata/*.mock.yaml'
