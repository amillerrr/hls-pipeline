# Makefile
.PHONY: all deploy destroy env

# Default target
all: deploy

# Deploy Infrastructure
deploy:
	@echo "Deploying Infrastructure..."
	@terraform -chdir=infra/environments/dev init
	@terraform -chdir=infra/environments/dev apply -auto-approve
	@$(MAKE) env
	@echo "Deployment Complete. Local .env updated."

# Generate .env file
env:
	@echo "Generating .env file..."
	@terraform -chdir=infra/environments/dev output -json \
		| jq -r 'to_entries | .[] | .key + "=" + .value.value' > .env
	@echo "AWS_REGION=us-west-2" >> .env

# Destroy Infrastructure
destroy:
	@echo "Destroying Infrastructure..."
	@terraform -chdir=infra/environments/dev destroy -auto-approve
	@rm -f .env
	@echo "Infrastructure Destroyed."
