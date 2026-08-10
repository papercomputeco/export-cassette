IMAGE ?= tapes/export-cassette:0.1.0

.PHONY: check
check: ## Runs the dagger checks
	dagger check

.PHONY: build
build: ## Builds the cassette binary
	go build -o build/export-cassette .

.PHONY: image
image: ## Builds and loads the cassette container image via Dagger
	dagger call build-image export-image --name=$(IMAGE)

.PHONY: check-image
check-image: ## Builds the cassette container image without loading it
	dagger call build-image sync

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
