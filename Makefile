.PHONY: test test-quick test-retransmit test-frag test-subtree-announce test-block test-bgp test-ssm test-dedup test-manifest test-coalesce test-beef test-misc test-one clean images help

GOTEST := sudo go test ./harness/scenarios/... -v -count=1

# ~60 scenarios run serially (only 3 use t.Parallel), each ~40-100s of
# container start + settle + generator + drain. The project's own per-group
# targets budget ~100s/scenario, so the full suite needs well over an hour.
#
# The themed test-* targets below form a COMPLETE, non-overlapping partition of
# the suite (misc + quick + retransmit + subtree-announce + frag + block + bgp +
# dedup + ssm + manifest + coalesce + beef == every scenario). Run them as
# separate steps to stay under a per-step timeout; keep them exhaustive when
# adding scenarios so nothing is silently skipped.
test: ## Run all harness scenarios (requires sudo)
	$(GOTEST) -timeout 90m

test-quick: ## Run only tier-1 filter scenarios (~60s)
	$(GOTEST) -timeout 5m -run 'Scenario0[1-3]|Scenario0[67]'

test-retransmit: ## Run NACK/retransmit scenarios
	# trailing _ keeps 1[0-9] from also matching Scenario100 (beef group)
	$(GOTEST) -timeout 20m -run 'Scenario(99|08|1[0-9]_)'

test-frag: ## Run fragmentation scenarios
	$(GOTEST) -timeout 10m -run 'Scenario2[2-6]'

test-subtree-announce: ## Run BRC-127 subtree announce scenarios
	$(GOTEST) -timeout 5m -run 'Scenario2[01]'

test-block: ## Run BRC-131/132/134/135 block / subtree / anchor / header scenarios
	$(GOTEST) -timeout 15m -run 'Scenario3[0-7]'

test-bgp: ## Run BGP scenarios
	$(GOTEST) -timeout 10m -run 'Scenario4[0-2]'

test-dedup: ## Run TxID dedup scenarios
	$(GOTEST) -timeout 10m -run 'Scenario5[0-3]'

test-ssm: ## Run SSM scenarios (RFC 4607)
	$(GOTEST) -timeout 5m -run 'Scenario6[01]'

test-manifest: ## Run BRC-139 manifest / auto-shard-config scenarios
	$(GOTEST) -timeout 10m -run 'Scenario7[0-5]'

test-coalesce: ## Run BRC-142 coalescing / bundle scenarios
	$(GOTEST) -timeout 12m -run 'Scenario(89|9[01])'

test-beef: ## Run BRC-148 BEEF object plane scenarios
	$(GOTEST) -timeout 30m -run 'Scenario(9[2-8]|100)'

test-misc: ## Run baseline scenarios not in a themed group (firewall, dashboard, egress bridge, payload verify)
	$(GOTEST) -timeout 10m -run 'Scenario0[0459]'

test-one: ## Run a single scenario test by name: make test-one T=Scenario36
	@if [ -z "$(T)" ]; then echo "usage: make test-one T=<TestName>"; exit 2; fi
	$(GOTEST) -timeout 15m -run '^$(T)$$'

clean: ## Remove harness containers and network
	@sudo docker ps -a --filter 'name=^s[0-9]' --format '{{.Names}}' | xargs -r sudo docker rm -f 2>/dev/null || true
	@sudo docker network rm mcast-fabric 2>/dev/null || true
	@echo "cleaned up containers and network"

images: ## Force rebuild all harness images
	@sudo docker images --filter reference='*:harness' -q | xargs -r sudo docker rmi -f 2>/dev/null || true
	@echo "removed harness images; they will rebuild on next test run"

help: ## Show this help
	@grep -E '^[a-z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-20s %s\n", $$1, $$2}'
