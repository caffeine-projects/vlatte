# Static success responses; writes, filters and authentication are not simulated.
.PHONY: example\:petstore
example\:petstore: ## run the Pet Store mock API on 127.0.0.1:8080
	@go run ./cmd/vlatte --addr=127.0.0.1:8080 --workdir=.local/petstore --mock-files='examples/01-petstore/testdata/*.mock.yaml'
