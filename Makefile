# Makefile
.PHONY: all deploy destroy env

# Default target
all: deploy

# 1. Deploy Infrastructure
deploy:
	@echo "Deploying Infrastructure..."
	@terraform -chdir=infra/environments/dev init
	@terraform -chdir=infra/environments/dev apply -auto-approve
	@$(MAKE) env
	@echo "Deployment Complete. Local .env updated."

# 2. Generate .env file (The Magic Command)
env:
	@echo "Generating .env file..."
	@terraform -chdir=infra/environments/dev output -json \
		| jq -r 'to_entries | .[] | .key + "=" + .value.value' > .env
	@echo "AWS_REGION=us-west-2" >> .env

# 3. Destroy Infrastructure
destroy:
	@echo "Destroying Infrastructure..."
	@terraform -chdir=infra/environments/dev destroy -auto-approve
	@rm -f .env
	@echo "Infrastructure Destroyed."
