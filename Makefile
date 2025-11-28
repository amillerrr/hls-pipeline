# Makefile
.PHONY: all deploy destroy env

# Configuration
AWS_REGION ?= us-west-2
TF_DIR = infra/environments/dev
BOOTSTRAP_DIR = infra/bootstrap

# ECR Configuration (populated after deploy)
-include .env
ECR_API_URL ?= $(shell terraform -chdir=$(TF_DIR) output -raw api_repository_url 2>/dev/null || echo "")
ECR_WORKER_URL ?= $(shell terraform -chdir=$(TF_DIR) output -raw worker_repository_url 2>/dev/null || echo "")

# Default target
all: help

# Create S3 state bucket and DynamoDB lock table 
bootstrap:
	@echo "Creating Terraform state infrastructure..."
	@aws s3api create-bucket \
		--bucket eye-tf-state-store \
		--region $(AWS_REGION) \
		--create-bucket-configuration LocationConstraint=$(AWS_REGION) 2>/dev/null || true
	@aws s3api put-bucket-versioning \
		--bucket eye-tf-state-store \
		--versioning-configuration Status=Enabled
	@aws s3api put-bucket-encryption \
		--bucket eye-tf-state-store \
		--server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
	@aws dynamodb create-table \
		--table-name eye-tf-lock-table \
		--attribute-definitions AttributeName=LockID,AttributeType=S \
		--key-schema AttributeName=LockID,KeyType=HASH \
		--billing-mode PAY_PER_REQUEST \
		--region $(AWS_REGION) 2>/dev/null || true
	@echo "Bootstrap complete"

# Initialize Terraform
init:
	@echo "Initializing Terraform..."
	@terraform -chdir=$(TF_DIR) init

## plan: Show Terraform execution plan
plan: init
	@echo "Planning infrastructure changes..."
	@terraform -chdir=$(TF_DIR) plan

# Deploy Infrastructure
deploy: init
	@echo "Deploying infrastructure..."
	@terraform -chdir=$(TF_DIR) apply -auto-approve
	@$(MAKE) env
	@echo ""
	@echo "Deployment Complete."

# Destroy Infrastructure
destroy:
	@echo "Destroying Infrastructure..."
	@terraform -chdir=$(TF_DIR) destroy -auto-approve
	@rm -f .env
	@echo "Infrastructure Destroyed."

# Generate .env file from Terraform outputs
env:
	@echo "Generating .env file..."
	@terraform -chdir=$(TF_DIR) output -json 2>/dev/null \
		| jq -r 'to_entries | .[] | select(.value.value != null) | .key + "=" + (.value.value | tostring)' > .env
	@echo "AWS_REGION=$(AWS_REGION)" >> .env
	@echo ".env file created"

# Show Terraform outputs
outputs:
	@terraform -chdir=$(TF_DIR) output


# Build Targets

# Build Docker images locally
build: build-api build-worker

# Build API Docker image
build-api:
	@echo "Building API image..."
	@docker build -t eye-api:latest -f Dockerfile .
	@echo "API image built"

# Build Worker Docker image
build-worker:
	@echo "Building Worker image..."
	@docker build -t eye-worker:latest -f Dockerfile.worker .
	@echo "Worker image built"

# Push images to ECR
push: push-api push-worker

# Push API image to ECR
push-api:
	@echo "Pushing API image to ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_API_URL)
	@docker tag eye-api:latest $(ECR_API_URL):latest
	@docker push $(ECR_API_URL):latest
	@echo "API image pushed"

# Push Worker image to ECR
push-worker:
	@echo "Pushing Worker image to ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_WORKER_URL)
	@docker tag eye-worker:latest $(ECR_WORKER_URL):latest
	@docker push $(ECR_WORKER_URL):latest
	@echo "Worker image pushed"

# Force new ECS deployment 
ecs-deploy:
	@echo "Forcing ECS service update..."
	@aws ecs update-service --cluster eye-cluster --service eye-api-svc --force-new-deployment --region $(AWS_REGION) > /dev/null
	@aws ecs update-service --cluster eye-cluster --service eye-worker-svc --force-new-deployment --region $(AWS_REGION) > /dev/null
	@echo "ECS services updating."

# Development Targets
# Run Go linters
lint:
	@echo "Running linters..."
	@golangci-lint run ./... || true

# Run Go tests
test:
	@echo "Running tests..."
	@go test -v -race -coverprofile=coverage.out ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f .env coverage.out
	@rm -rf tmp/
	@docker rmi eye-api:latest eye-worker:latest 2>/dev/null || true
	@echo "Clean complete"

# Local Development

# Start local development environment
local-up:
	@echo "Starting local environment..."
	@mkdir -p configs scripts/localstack-init
	@test -f configs/prometheus-local.yaml || cp prometheus-local.yml configs/prometheus-local.yaml 2>/dev/null || echo "⚠️  Create configs/prometheus-local.yaml"
	@test -f configs/otel-collector-local.yaml || echo "⚠️  Create configs/otel-collector-local.yaml"
	@docker-compose -f docker-compose.local.yml up -d
	@echo "Local environment started"
	@echo "   API:        http://localhost:8080"
	@echo "   Jaeger:     http://localhost:16686"
	@echo "   Prometheus: http://localhost:9090"

# Stop local development environment
local-down:
	@echo "Stopping local environment..."
	@docker-compose -f docker-compose.local.yml down -v
	@echo "Local environment stopped"

# View local container logs
local-logs:
	@docker-compose -f docker-compose.local.yml logs -f

# Utility Targets 

# Upload a test video (requires test_assets/tempest_input.mp4)
upload-test:
	@./upload_video.sh

# Run stress test against API
stress-test:
	@./stress_test.sh

# Show this help
help:
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Infrastructure:"
	@grep -E '^## ' $(MAKEFILE_LIST) | grep -E '(bootstrap|init|plan|deploy|destroy|env|outputs):' | sed 's/## /  /' | column -t -s ':'
	@echo ""
	@echo "Build & Deploy:"
	@grep -E '^## ' $(MAKEFILE_LIST) | grep -E '(build|push|ecs-deploy):' | sed 's/## /  /' | column -t -s ':'
	@echo ""
	@echo "Development:"
	@grep -E '^## ' $(MAKEFILE_LIST) | grep -E '(lint|test|clean):' | sed 's/## /  /' | column -t -s ':'
	@echo ""
	@echo "Local Dev (Optional):"
	@grep -E '^## ' $(MAKEFILE_LIST) | grep -E 'local-' | sed 's/## /  /' | column -t -s ':'
	@echo ""
	@echo "Utilities:"
	@grep -E '^## ' $(MAKEFILE_LIST) | grep -E '(upload-test|stress-test|oidc-setup|help):' | sed 's/## /  /' | column -t -s ':'
	@echo ""

