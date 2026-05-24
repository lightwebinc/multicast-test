.PHONY: test test-quick test-retransmit test-frag test-bgp clean images help

GOTEST := sudo go test ./harness/scenarios/... -v -count=1

test: ## Run all harness scenarios (requires sudo)
	$(GOTEST) -timeout 30m

test-quick: ## Run only tier-1 filter scenarios (~60s)
	$(GOTEST) -timeout 5m -run 'Scenario0[1-3]|Scenario0[67]'

test-retransmit: ## Run NACK/retransmit scenarios
	$(GOTEST) -timeout 15m -run 'Scenario(99|1[0-6])'

test-frag: ## Run fragmentation scenarios
	$(GOTEST) -timeout 10m -run 'Scenario2[2-6]'

test-bgp: ## Run BGP scenarios
	$(GOTEST) -timeout 10m -run 'Scenario4[02]'

clean: ## Remove harness containers and network
	@sudo docker ps -a --filter 'name=^s[0-9]' --format '{{.Names}}' | xargs -r sudo docker rm -f 2>/dev/null || true
	@sudo docker network rm mcast-fabric 2>/dev/null || true
	@echo "cleaned up containers and network"

images: ## Force rebuild all harness images
	@sudo docker images --filter reference='*:harness' -q | xargs -r sudo docker rmi -f 2>/dev/null || true
	@echo "removed harness images; they will rebuild on next test run"

help: ## Show this help
	@grep -E '^[a-z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-20s %s\n", $$1, $$2}'
