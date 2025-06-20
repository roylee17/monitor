# Makefile for Interchain Security Monitor
# Provides build and deployment commands for the monitor service

# === Configuration ===
ICS_VERSION ?= v7.0.1

# Directories
K8S_DIR := k8s/testnet
SCRIPTS_DIR := scripts

# Docker settings
MONITOR_IMAGE := ics-monitor:latest
ICS_IMAGE := ghcr.io/cosmos/interchain-security:$(ICS_VERSION)

# Kubernetes settings
NAMESPACE := provider
CONSUMER_NAMESPACE := consumer-chains
VALIDATOR_COUNT := 3

# === Default target ===
.DEFAULT_GOAL := help

# === Phony targets ===
.PHONY: help all clean
.PHONY: docker-build docker-push
.PHONY: deploy deploy-testnet deploy-monitors reset reset-testnet reset-monitors
.PHONY: status logs shell monitor-events
.PHONY: create-consumer list-consumers verify-consumer consumer-info
.PHONY: dev dev-monitor dev-testnet
.PHONY: test-consumer-workflow
.PHONY: generate-testnet clean-generated testnet-quick testnet-idempotent
.PHONY: import-keys opt-in-all quick-test

# === Generation targets ===
generate-testnet: ## Generate testnet using genesis ceremony
	@echo "🔧 Generating testnet configuration..."
	@$(SCRIPTS_DIR)/testnet-coordinator.sh -t '2025-01-01T00:00:00Z' -s
	@$(SCRIPTS_DIR)/generate-k8s-manifests.sh
	@echo "✅ Testnet configuration and K8s manifests generated"

clean-generated: ## Clean generated testnet configuration files
	@echo "🧹 Cleaning generated testnet configuration..."
	@rm -rf $(K8S_DIR)/assets
	@rm -rf $(K8S_DIR)/keys-backup
	@rm -rf $(K8S_DIR)/generated
	@echo "✅ Generated files cleaned"

testnet-quick: ## Quick testnet setup with current time
	@echo "⚡ Quick testnet setup..."
	@$(SCRIPTS_DIR)/setup-testnet.sh -s

testnet-idempotent: ## Idempotent testnet with fixed time
	@echo "🔁 Idempotent testnet setup..."
	@$(SCRIPTS_DIR)/setup-testnet.sh -t '2025-01-01T00:00:00Z' -s

# === Help ===
help: ## Show this help message
	@echo "Interchain Security Monitor - Makefile"
	@echo "======================================"
	@echo ""
	@echo "Quick Start:"
	@echo "  make docker-build        # Build monitor Docker image"
	@echo "  make deploy              # Deploy 3-validator testnet with monitors"
	@echo "  make status              # Check deployment status"
	@echo "  make status-verbose      # Check detailed deployment status"
	@echo "  make create-consumer     # Create a test consumer chain"
	@echo ""
	@echo "All Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-25s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
	@echo ""
	@echo "Configuration:"
	@echo "  NAMESPACE=$(NAMESPACE)"
	@echo "  ICS_VERSION=$(ICS_VERSION)"
	@echo ""

# === Main targets ===
all: docker-build ## Build everything

clean: ## Clean all deployments
	@echo "🧹 Cleaning Kubernetes resources..."
	@kubectl delete namespace $(NAMESPACE) --ignore-not-found=true 2>/dev/null || true
	@kubectl delete namespace $(CONSUMER_NAMESPACE) --ignore-not-found=true 2>/dev/null || true
	@echo "✅ Clean complete"

# === Build targets ===
docker-build: ## Build monitor Docker image
	@echo "🐳 Building Docker image $(MONITOR_IMAGE)..."
	@docker build -t $(MONITOR_IMAGE) .
	@if command -v kind >/dev/null 2>&1 && kind get clusters >/dev/null 2>&1; then \
		echo "📦 Loading image into Kind cluster..."; \
		kind load docker-image $(MONITOR_IMAGE); \
	fi
	@echo "✅ Docker image ready"

docker-push: docker-build ## Push Docker image to registry
	@echo "📤 Pushing Docker image..."
	@docker push $(MONITOR_IMAGE)

# === Deployment targets ===
deploy: docker-build generate-testnet ## Deploy complete testnet with monitors
	@echo "🚀 Deploying complete testnet with monitors..."
	@# Apply generated Kubernetes manifests
	@kubectl apply -f $(K8S_DIR)/generated/
	@echo "⏳ Waiting for pods to be ready..."
	@kubectl wait --for=condition=ready pod -l role=validator -n $(NAMESPACE) --timeout=120s
	@kubectl wait --for=condition=ready pod -l role=monitor -n $(NAMESPACE) --timeout=60s || true
	@echo "✅ Complete deployment done!"
	@echo ""
	@echo "📝 Next steps:"
	@echo "   - View status: make status"
	@echo "   - Monitor events: make monitor-events"
	@echo "   - Create consumer chain: make create-consumer"

# Removed obsolete deploy-testnet target - use 'make deploy' instead

# Removed obsolete deploy-monitors target - monitors are deployed with 'make deploy'"

reset: ## Clean and redeploy everything
	@$(MAKE) -s clean
	@$(MAKE) -s deploy

reset-testnet: ## Reset only testnet (preserves monitor image)
	@echo "🔄 Resetting testnet..."
	@kubectl delete pods -l role=validator -n $(NAMESPACE) --force --grace-period=0 2>/dev/null || true
	@kubectl delete deploy -l role=validator -n $(NAMESPACE) 2>/dev/null || true
	@kubectl delete svc -l role=validator -n $(NAMESPACE) 2>/dev/null || true
	@$(MAKE) -s deploy

reset-monitors: ## Reset only monitors
	@echo "🔄 Resetting monitors..."
	@kubectl delete deploy -l app=monitor -n $(NAMESPACE) 2>/dev/null || true
	@$(MAKE) -s deploy-monitors

# === Status and monitoring ===
status: ## Check deployment status
	@NAMESPACE=$(NAMESPACE) ./scripts/status.sh

status-provider: ## Show only provider status
	@NAMESPACE=$(NAMESPACE) ./scripts/status.sh --provider

status-consumers: ## Show only consumer chains status
	@NAMESPACE=$(NAMESPACE) ./scripts/status.sh --consumers

status-verbose: ## Show detailed status
	@NAMESPACE=$(NAMESPACE) ./scripts/status.sh --verbose

status-json: ## Output status as JSON
	@NAMESPACE=$(NAMESPACE) ./scripts/status.sh --json

logs: ## View logs (TARGET=validator-alice|monitor|all)
	@TARGET=$${TARGET:-all}; \
	if [ "$$TARGET" = "all" ]; then \
		echo "📜 Showing recent logs from all components..."; \
		for component in validator-alice validator-bob validator-charlie; do \
			echo ""; \
			echo "=== $$component ==="; \
			kubectl -n $(NAMESPACE) logs deploy/$$component -c validator --tail=5 2>/dev/null || echo "Not running"; \
		done; \
		echo ""; \
		echo "=== monitor ==="; \
		kubectl -n $(NAMESPACE) logs deploy/monitor -c monitor --tail=5 2>/dev/null || echo "Not running"; \
	else \
		if [ "$$TARGET" = "monitor" ]; then \
			echo "📜 Following logs for $$TARGET..."; \
			kubectl -n $(NAMESPACE) logs deploy/$$TARGET -c monitor -f; \
		else \
			echo "📜 Following logs for $$TARGET..."; \
			kubectl -n $(NAMESPACE) logs deploy/$$TARGET -c validator -f; \
		fi; \
	fi

shell: ## Get shell access (TARGET=validator-alice|validator-bob|validator-charlie)
	@TARGET=$${TARGET:-validator-alice}; \
	echo "🐚 Connecting to $$TARGET..."; \
	kubectl -n $(NAMESPACE) exec -it deploy/$$TARGET -c validator -- /bin/bash

monitor-events: ## Monitor CCV events in real-time
	@echo "👁️  Monitoring CCV events..."
	@echo "Press Ctrl+C to stop"
	@echo ""
	@kubectl -n $(NAMESPACE) logs -l app=monitor -c monitor -f --tail=0 | grep -E "(create_consumer|update_consumer|opt_in|assign_consumer_key|LAUNCHED|spawn_time)"


# === Consumer chain operations ===
create-consumer: ## Create a test consumer chain with auto-generated ID
	@echo "📝 Creating consumer chain..."
	@$(SCRIPTS_DIR)/lifecycle/create-consumer.sh -s 30

list-consumers: ## List all consumer chains
	@echo "📋 Consumer Chains"
	@echo "=================="
	@kubectl -n $(NAMESPACE) exec deploy/validator-alice -c validator -- \
		interchain-security-pd query provider list-consumer-chains \
		--home /chain/.provider \
		--output json 2>/dev/null | \
		jq -r '.chains[]? | "ID: \(.consumer_id // "N/A") | Chain: \(.chain_id) | Phase: \(.phase)"' || echo "No consumer chains found"

verify-consumer: ## Verify consumer chain deployment (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	echo "🔍 Verifying consumer chain $$CONSUMER_ID..."; \
	echo ""; \
	echo "=== Chain Info ==="; \
	kubectl -n $(NAMESPACE) exec deploy/validator-alice -- \
		interchain-security-pd query provider consumer-chain $$CONSUMER_ID \
		--home /chain/.provider --output json 2>/dev/null | \
		jq -r '. | "Chain ID: \(.chain_id)\nPhase: \(.phase)\nOwner: \(.owner_address)"' || echo "Not found"; \
	echo ""; \
	echo "=== Opted-in Validators ==="; \
	kubectl -n $(NAMESPACE) exec deploy/validator-alice -- \
		interchain-security-pd query provider opted-in-validators $$CONSUMER_ID \
		--home /chain/.provider 2>/dev/null || echo "None"; \
	echo ""; \
	echo "=== Consumer Deployments ==="; \
	kubectl -n $(CONSUMER_NAMESPACE) get deployments -l consumer-id=$$CONSUMER_ID 2>/dev/null || echo "No deployments found"

# === Development workflows ===
dev: ## Quick development cycle (build, deploy, test)
	@echo "🔄 Development Cycle"
	@echo "==================="
	@$(MAKE) -s docker-build
	@$(MAKE) -s reset
	@sleep 10
	@$(MAKE) -s status
	@echo ""
	@echo "✅ Ready for development"
	@echo "   - View logs: make logs TARGET=monitor-alice"
	@echo "   - Monitor events: make monitor-events"

dev-monitor: ## Fast monitor development cycle (rebuild and redeploy monitors only)
	@echo "🔄 Monitor Development Cycle"
	@echo "==========================="
	@echo "🐳 Building Docker image..."
	@$(MAKE) -s docker-build
	@echo "🔄 Redeploying monitors..."
	@$(MAKE) -s reset-monitors
	@echo ""
	@echo "✅ Monitors updated and running"
	@echo "   - View logs: make logs TARGET=monitor-alice"
	@echo "   - Monitor events: make monitor-events"

dev-testnet: ## Setup fresh testnet for development
	@echo "🔄 Setting up fresh testnet"
	@echo "=========================="
	@$(MAKE) -s reset-testnet
	@echo ""
	@echo "✅ Fresh testnet ready"
	@echo "   - Status: make status"
	@echo "   - Deploy monitors: make deploy-monitors"

test-consumer-workflow: ## Test complete consumer chain workflow
	@echo "🧪 Testing Consumer Chain Workflow"
	@echo "================================="
	@echo ""
	@echo "Running automated test..."
	@$(SCRIPTS_DIR)/lifecycle/test-consumer-lifecycle.sh

# Removed obsolete import-keys target - keys are now imported during validator initialization

opt-in-all: ## Opt-in all validators to consumer chain (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	echo "🤝 Opting in all validators to consumer chain $$CONSUMER_ID..."; \
	kubectl -n $(NAMESPACE) exec deploy/validator-alice -- interchain-security-pd tx provider opt-in $$CONSUMER_ID --from alice --keyring-backend test --chain-id provider-1 --yes >/dev/null 2>&1 && echo "  ✓ Alice opted in"; \
	kubectl -n $(NAMESPACE) exec deploy/validator-bob -- interchain-security-pd tx provider opt-in $$CONSUMER_ID --from bob --keyring-backend test --chain-id provider-1 --yes >/dev/null 2>&1 && echo "  ✓ Bob opted in"; \
	kubectl -n $(NAMESPACE) exec deploy/validator-charlie -- interchain-security-pd tx provider opt-in $$CONSUMER_ID --from charlie --keyring-backend test --chain-id provider-1 --yes >/dev/null 2>&1 && echo "  ✓ Charlie opted in"; \
	echo "✅ All validators opted in"

quick-test: ## Quick test: create consumer chain with all validators
	@echo "🚀 Quick test: Creating consumer chain with all validators..."
	@$(SCRIPTS_DIR)/lifecycle/create-consumer.sh -s 30
	@sleep 2
	@CONSUMER_ID=$$(kubectl -n $(NAMESPACE) exec deploy/validator-alice -- interchain-security-pd query provider list-consumer-chains -o json 2>/dev/null | jq -r '.chains[-1].consumer_id'); \
	echo "  Created consumer ID: $$CONSUMER_ID"; \
	$(MAKE) -s opt-in-all CONSUMER_ID=$$CONSUMER_ID
	@echo ""
	@echo "✅ Test setup complete! Monitor events with: make monitor-events"

show-consumer: ## Show detailed consumer chain info (CONSUMER_ID=0)
	@CONSUMER_ID=$${CONSUMER_ID:-0}; \
	$(SCRIPTS_DIR)/lifecycle/list-consumers.sh $$CONSUMER_ID
