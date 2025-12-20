.PHONY: build test lint clean docker-build docker-push help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

# Binary names
API_BINARY=api
WORKER_BINARY=worker

# Docker parameters
DOCKER_REGISTRY?=your-account.dkr.ecr.us-west-2.amazonaws.com
IMAGE_TAG?=latest

# Build directories
BUILD_DIR=build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

all: clean lint test build ## Run clean, lint, test, and build

build: build-api build-worker ## Build all binaries

build-api: ## Build the API binary
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) -ldflags="-s -w" -o $(BUILD_DIR)/$(API_BINARY) ./cmd/api

build-worker: ## Build the Worker binary
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) -ldflags="-s -w" -o $(BUILD_DIR)/$(WORKER_BINARY) ./cmd/worker

test: ## Run tests
	$(GOTEST) -v -race -cover ./...

test-coverage: ## Run tests with coverage report
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

lint: ## Run linters
	$(GOVET) ./...
	$(GOFMT) ./...
	@if command -v golangci-lint &> /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping"; \
	fi

fmt: ## Format code
	$(GOFMT) ./...

clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

docker-build: ## Build Docker images
	docker build -t $(DOCKER_REGISTRY)/hls-api:$(IMAGE_TAG) -f Dockerfile.api .
	docker build -t $(DOCKER_REGISTRY)/hls-worker:$(IMAGE_TAG) -f Dockerfile.worker .

docker-push: ## Push Docker images to registry
	docker push $(DOCKER_REGISTRY)/hls-api:$(IMAGE_TAG)
	docker push $(DOCKER_REGISTRY)/hls-worker:$(IMAGE_TAG)

run-api: ## Run the API locally
	$(GOCMD) run ./cmd/api

run-worker: ## Run the Worker locally
	$(GOCMD) run ./cmd/worker

# Development helpers
dev-setup: ## Setup development environment
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Done!"

check: lint test ## Run all checks (lint + test)
