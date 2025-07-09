# Makefile for Interchain Security Monitor
# Provides build and deployment commands for the monitor service

# ============================================
# Configuration
# ============================================

# Shell configuration for error handling
.SHELLFLAGS := -ec
ICS_VERSION ?= v7.0.1

# Directories
DEVNET_DIR := ./devnet
SCRIPTS_DIR := scripts

# Docker settings
MONITOR_IMAGE := ics-monitor:latest
ICS_IMAGE := ghcr.io/cosmos/interchain-security:$(ICS_VERSION)

# Kubernetes settings
NAMESPACE := provider
VALIDATOR_COUNT := 3

# ============================================
# Default target
# ============================================
.DEFAULT_GOAL := help

# ============================================
# Phony targets
# ============================================
.PHONY: help clean clean-consumers clean-assets quick-start
.PHONY: docker-build
.PHONY: generate-devnet create-clusters delete-clusters deploy reset register-endpoints
.PHONY: status status-verbose logs shell
.PHONY: create-consumer list-consumers show-consumer remove-consumer
.PHONY: show-consumer-genesis show-consumer-keys consumer-info consumer-logs

# ============================================
# Help
# ============================================
help: ## Show this help message
	@echo "Interchain Security Monitor - Makefile"
	@echo "======================================"
	@echo ""
	@echo "Quick Start:"
	@echo "  make quick-start         # Complete setup with consumer chain (5 mins)"
	@echo "  make deploy              # Full deployment: create clusters, build, and deploy"
	@echo "  make status              # Check deployment status"
	@echo "  make create-consumer     # Create a test consumer chain"
	@echo ""
	@echo "Cluster Management:"
	@echo "  make create-clusters     # Create 3 Kind clusters"
	@echo "  make delete-clusters     # Delete all Kind clusters"
	@echo "  make reset              # Full reset: delete and recreate everything"
	@echo ""
	@echo "All Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-25s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort | grep -v "^  _"
	@echo ""
	@echo "Configuration:"
	@echo "  NAMESPACE=$(NAMESPACE)"
	@echo "  ICS_VERSION=$(ICS_VERSION)"
	@echo ""

# ============================================
# Cleanup targets
# ============================================
clean: delete-clusters clean-assets ## Clean everything

clean-assets: ## Clean generated assets and manifests
	@echo "🧹 Cleaning generated files..."
	@rm -rf $(DEVNET_DIR)/assets
	@echo "✅ Clean complete"

clean-consumers: ## Clean consumer chain namespaces
	@echo "🧹 Cleaning consumer chain namespaces..."
	@for cluster in alice bob charlie; do \
		kubectl --context "kind-$${cluster}-cluster" get ns -l app.kubernetes.io/part-of=consumer-chains --no-headers 2>/dev/null | \
		cut -d' ' -f1 | xargs -r kubectl --context "kind-$${cluster}-cluster" delete ns --ignore-not-found=true 2>/dev/null || true; \
	done
	@echo "✅ Consumer chains cleaned"

# ============================================
# Build targets
# ============================================
docker-build: ## Build monitor Docker image
	@echo "🐳 Building Docker image $(MONITOR_IMAGE)..."
	@DOCKER_BUILDKIT=1 docker build -t $(MONITOR_IMAGE) .
	@echo "✅ Docker image ready"

# ============================================
# Deployment targets
# ============================================
generate-devnet: ## Generate devnet configuration
	@echo "⚙️  Generating devnet configuration..."
	@$(SCRIPTS_DIR)/devnet/generate-devnet.sh -t '2025-01-01T00:00:00Z' -s
	@echo "✅ Devnet configuration generated"

create-clusters: ## Create 3 Kind clusters
	@echo "🌐 Creating 3 Kind clusters..."
	@$(SCRIPTS_DIR)/clusters/create-clusters.sh
	@echo "✅ Clusters created successfully"

delete-clusters: ## Delete all Kind clusters
	@echo "🗑️  Deleting all Kind clusters..."
	@$(SCRIPTS_DIR)/clusters/delete-clusters.sh
	@echo "✅ Clusters deleted successfully"

deploy: create-clusters docker-build generate-devnet ## Full deployment
	@echo "🚀 Deploying devnet with Helm..."
	@$(SCRIPTS_DIR)/deploy-devnet-helm.sh
	@echo ""
	@echo "📝 Next steps:"
	@echo "   - View status: make status"
	@echo "   - Register validator endpoints: make register-endpoints"
	@echo "   - Create consumer chain: make create-consumer"

register-endpoints: ## Register validator P2P endpoints on chain
	@echo "📝 Registering validator P2P endpoints..."
	@$(SCRIPTS_DIR)/devnet/register-validator-endpoints.sh

reset: ## Full reset and redeploy
	@$(MAKE) -s delete-clusters
	@$(MAKE) -s clean-assets
	@$(MAKE) -s deploy

deploy-metallb:
	@echo "📦 Installing MetalLB for LoadBalancer support..."
	@$(SCRIPTS_DIR)/clusters/install-metallb.sh

quick-start: ## Complete setup with consumer chain (automated flow)
	@$(MAKE) -s deploy
	@$(MAKE) -s deploy-metallb
	@$(MAKE) -s register-endpoints
	@$(MAKE) -s create-consumer
	@echo "⏳ Show on-chain consumer status..."
	@$(MAKE) -s show-consumer CONSUMER_ID=0
	@echo "⏳ Waiting for consumer chain to launch (20s)..."

	@sleep 20
	@echo "✅ Quick start complete! Checking runtime consumer chain status..."
	@$(MAKE) -s consumer-info CONSUMER_ID=0

# ============================================
# Status and monitoring
# ============================================
status: ## Check deployment status
	@echo "📊 Cluster status:"
	@for cluster in alice bob charlie; do \
		echo ""; \
		echo "=== $$(echo $$cluster | tr '[:lower:]' '[:upper:]') CLUSTER ==="; \
		kubectl --context "kind-$${cluster}-cluster" -n $(NAMESPACE) get pods 2>/dev/null || echo "Cluster not found"; \
	done
	@echo ""
	@echo "🔗 Peer connections:"
	@for cluster in alice bob charlie; do \
		printf "%-10s: " "$$cluster"; \
		kubectl --context "kind-$${cluster}-cluster" -n $(NAMESPACE) exec deploy/validator -- \
			sh -c "curl -s http://localhost:26657/net_info | jq -r '.result.n_peers // \"disconnected\"'" 2>/dev/null || echo "error"; \
	done

status-verbose: ## Detailed deployment status
	@$(MAKE) -s status
	@echo ""
	@echo "📊 Validator sync status:"
	@for cluster in alice bob charlie; do \
		echo ""; \
		echo "=== $$cluster ==="; \
		kubectl --context "kind-$${cluster}-cluster" -n $(NAMESPACE) exec deploy/validator -- \
			curl -s http://localhost:26657/status 2>/dev/null | \
			jq -r '.result.sync_info | "Height: \(.latest_block_height)\nTime: \(.latest_block_time)"' || echo "Error getting status"; \
	done
	@echo ""
	@echo "🌐 Consumer chains:"
	@kubectl --context "kind-alice-cluster" -n $(NAMESPACE) exec deploy/validator -- \
		interchain-security-pd query provider list-consumer-chains --output json 2>/dev/null | \
		jq -r '.chains[] | "\(.chain_id): \(.phase)"' || echo "No consumer chains found"

logs: ## View logs (TARGET=alice|bob|charlie, COMPONENT=validator|monitor)
	@CLUSTER=$${TARGET:-alice}; \
	COMPONENT=$${COMPONENT:-validator}; \
	echo "📜 Following logs for $$COMPONENT in $$CLUSTER cluster..."; \
	kubectl --context "kind-$${CLUSTER}-cluster" -n $(NAMESPACE) logs -f -l app.kubernetes.io/component=$$COMPONENT

shell: ## Get shell access (TARGET=alice|bob|charlie)
	@CLUSTER=$${TARGET:-alice}; \
	echo "🐚 Connecting to validator in $$CLUSTER cluster..."; \
	kubectl --context "kind-$${CLUSTER}-cluster" -n $(NAMESPACE) exec -it deploy/validator -- /bin/bash

# ============================================
# Consumer chain operations
# ============================================
create-consumer: ## Create a test consumer chain
	@echo "📝 Creating consumer chain..."
	@$(SCRIPTS_DIR)/lifecycle/create-consumer.sh -s 10

remove-consumer: ## Remove a consumer chain (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	echo "🗑️  Removing consumer chain $$CONSUMER_ID..."; \
	$(SCRIPTS_DIR)/lifecycle/remove-consumer.sh "$$CONSUMER_ID"

list-consumers: ## List all consumer chains
	@$(SCRIPTS_DIR)/lifecycle/list-consumers.sh

show-consumer: ## Show consumer chain info (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	$(SCRIPTS_DIR)/lifecycle/list-consumers.sh "$$CONSUMER_ID"

# ============================================
# Consumer chain detailed queries
# ============================================
show-consumer-genesis: ## Show consumer genesis (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	echo "🔍 Showing genesis for consumer $$CONSUMER_ID..."; \
	kubectl --context "kind-alice-cluster" -n $(NAMESPACE) exec deploy/validator -c validator -- \
		interchain-security-pd query provider consumer-genesis "$$CONSUMER_ID" \
		--home /chain/.provider -o json 2>/dev/null | jq '.' || echo "Consumer not found"

show-consumer-keys: ## Show consumer key assignments (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	echo "🔑 Consumer keys for consumer $$CONSUMER_ID:"; \
	for cluster in alice bob charlie; do \
		kubectl --context "kind-$${cluster}-cluster" get configmap -n $(NAMESPACE) "consumer-keys-$$CONSUMER_ID" -o json 2>/dev/null | \
		jq -r '.data | to_entries[] | "\(.key): \(.value | fromjson | {validator_name, consumer_pub_key, provider_address})"' || true; \
	done | sort -u || echo "No consumer keys found for consumer $$CONSUMER_ID"

consumer-info: ## Show comprehensive consumer chain info (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	$(SCRIPTS_DIR)/monitoring/consumer-info.sh $$CONSUMER_ID

consumer-logs: ## Show consumer chain logs (CONSUMER_ID=0 CLUSTER=bob)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	CLUSTER=$${CLUSTER:-bob}; \
	$(SCRIPTS_DIR)/monitoring/consumer-logs.sh $$CONSUMER_ID $$CLUSTER
