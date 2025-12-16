# Makefile - HLS Pipeline (AWS)
.PHONY: all deploy destroy env help

# Configuration
AWS_REGION ?= us-west-2
TF_DIR = infra/environments/dev

# ECR Configuration (populated after deploy)
-include .env
ECR_API_URL ?= $(shell terraform -chdir=$(TF_DIR) output -raw api_repository_url 2>/dev/null || echo "")
ECR_WORKER_URL ?= $(shell terraform -chdir=$(TF_DIR) output -raw worker_repository_url 2>/dev/null || echo "")

# Default target
all: help

## bootstrap: Create S3 state bucket and DynamoDB lock table
bootstrap:
	@echo "Creating Terraform state infrastructure..."
	@aws s3api create-bucket \
		--bucket hls-tf-state-store \
		--region $(AWS_REGION) \
		--create-bucket-configuration LocationConstraint=$(AWS_REGION) 2>/dev/null || true
	@aws s3api put-bucket-versioning \
		--bucket hls-tf-state-store \
		--versioning-configuration Status=Enabled
	@aws s3api put-bucket-encryption \
		--bucket hls-tf-state-store \
		--server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
	@aws dynamodb create-table \
		--table-name hls-tf-lock-table \
		--attribute-definitions AttributeName=LockID,AttributeType=S \
		--key-schema AttributeName=LockID,KeyType=HASH \
		--billing-mode PAY_PER_REQUEST \
		--region $(AWS_REGION) 2>/dev/null || true
	@echo "Bootstrap complete"

## init: Initialize Terraform
init:
	@echo "Initializing Terraform..."
	@terraform -chdir=$(TF_DIR) init

## plan: Show Terraform execution plan
plan: init
	@echo "Planning infrastructure changes..."
	@terraform -chdir=$(TF_DIR) plan

## deploy: Deploy infrastructure with Terraform
deploy: init
	@echo "Deploying infrastructure..."
	@terraform -chdir=$(TF_DIR) apply -auto-approve
	@$(MAKE) env
	@echo ""
	@echo "Deployment Complete."

## destroy: Destroy all infrastructure
destroy:
	@echo "Destroying Infrastructure..."
	@terraform -chdir=$(TF_DIR) destroy -auto-approve
	@rm -f .env
	@echo "Infrastructure Destroyed."

## env: Generate .env file from Terraform outputs
env:
	@echo "Generating .env file..."
	@terraform -chdir=$(TF_DIR) output -json 2>/dev/null \
		| jq -r 'to_entries | .[] | select(.value.value != null) | .key + "=" + (.value.value | tostring)' > .env
	@echo "AWS_REGION=$(AWS_REGION)" >> .env
	@echo ".env file created"

## outputs: Show Terraform outputs
outputs:
	@terraform -chdir=$(TF_DIR) output


# Build Targets

## build: Build all Docker images locally
build: build-api build-worker

## build-api: Build API Docker image
build-api:
	@echo "Building API image..."
	@docker build -t hls-api:latest -f Dockerfile .
	@echo "API image built"

## build-worker: Build Worker Docker image
build-worker:
	@echo "Building Worker image..."
	@docker build -t hls-worker:latest -f Dockerfile.worker .
	@echo "Worker image built"

## push: Push all images to ECR
push: push-api push-worker

## push-api: Push API image to ECR
push-api:
	@echo "Pushing API image to ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_API_URL)
	@docker tag hls-api:latest $(ECR_API_URL):latest
	@docker push $(ECR_API_URL):latest
	@echo "API image pushed"

## push-worker: Push Worker image to ECR
push-worker:
	@echo "Pushing Worker image to ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_WORKER_URL)
	@docker tag hls-worker:latest $(ECR_WORKER_URL):latest
	@docker push $(ECR_WORKER_URL):latest
	@echo "Worker image pushed"

## ecs-deploy: Force new ECS deployment
ecs-deploy:
	@echo "Forcing ECS service update..."
	@aws ecs update-service --cluster hls-cluster --service hls-api-svc --force-new-deployment --region $(AWS_REGION) > /dev/null
	@aws ecs update-service --cluster hls-cluster --service hls-worker-svc --force-new-deployment --region $(AWS_REGION) > /dev/null
	@echo "ECS services updating."

# Development Targets

## lint: Run Go linters
lint:
	@echo "Running linters..."
	@golangci-lint run ./...

## test: Run Go tests
test:
	@echo "Running tests..."
	@go test -v -race -coverprofile=coverage.out ./...

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f .env coverage.out
	@rm -rf tmp/
	@docker rmi hls-api:latest hls-worker:latest 2>/dev/null || true
	@echo "Clean complete"

## tidy: Run go mod tidy
tidy:
	@echo "Tidying Go modules..."
	@go mod tidy
	@echo "Done"

# Utility Targets 

## upload-test: Upload a test video (requires test_assets/tempest_input.mp4)
upload-test:
	@./upload_video.sh

## stress-test: Run stress test against API
stress-test:
	@./stress_test.sh

## help: Show this help message
help:
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Infrastructure:"
	@echo "  bootstrap     Create S3 state bucket and DynamoDB lock table"
	@echo "  init          Initialize Terraform"
	@echo "  plan          Show Terraform execution plan"
	@echo "  deploy        Deploy infrastructure with Terraform"
	@echo "  destroy       Destroy all infrastructure"
	@echo "  env           Generate .env file from Terraform outputs"
	@echo "  outputs       Show Terraform outputs"
	@echo ""
	@echo "Build & Deploy:"
	@echo "  build         Build all Docker images locally"
	@echo "  build-api     Build API Docker image"
	@echo "  build-worker  Build Worker Docker image"
	@echo "  push          Push all images to ECR"
	@echo "  push-api      Push API image to ECR"
	@echo "  push-worker   Push Worker image to ECR"
	@echo "  ecs-deploy    Force new ECS deployment"
	@echo ""
	@echo "Development:"
	@echo "  lint          Run Go linters"
	@echo "  test          Run Go tests"
	@echo "  tidy          Run go mod tidy"
	@echo "  clean         Clean build artifacts"
	@echo ""
	@echo "Utilities:"
	@echo "  upload-test   Upload a test video"
	@echo "  stress-test   Run stress test against API"
	@echo "  help          Show this help message"
	@echo ""
