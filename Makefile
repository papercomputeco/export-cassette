IMAGE ?= tapes/export-cassette:0.1.0
CONTAINER_TOOL ?= docker

.PHONY: check
check: ## Runs the dagger checks
	dagger check

.PHONY: build
build: image ## Builds the local development image directly from Dockerfile

.PHONY: build-local
build-local: ## Builds the cassette binary for the host
	mkdir -p build
	go build -o build/export-cassette .

.PHONY: image
image: ## Builds the local development image (override with IMAGE=name:tag)
	$(CONTAINER_TOOL) build -t $(IMAGE) -f Dockerfile .

.PHONY: check-image
check-image: image ## Compatibility alias for the former Dagger image check

.PHONY: test
test: ## Vets and tests
	go vet ./...
	go test ./...

.PHONY: format
format: ## Formats and organizes imports
	gofmt -w .
	goimports -w .

.PHONY: clean
clean: ## Removes built artifacts
	rm -rf build/

.PHONY: help
.DEFAULT_GOAL := help
help: ## Prints this help message
	@grep -h -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
