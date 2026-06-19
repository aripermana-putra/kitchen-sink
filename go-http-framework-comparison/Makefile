VARIANTS := 01-echo-standard 02-echo-strict 03-chi-standard 04-chi-strict 05-gin-standard 06-gin-strict

# ── Build ─────────────────────────────────────────────────────────────────

.PHONY: build
build: ## Build all 6 variants
	@for v in $(VARIANTS); do \
		echo "==> $$v"; \
		cd $$v && go build -o server . && cd ..; \
	done

.PHONY: generate
generate: ## Regenerate oapi-codegen for strict variants
	@for v in 02-echo-strict 04-chi-strict 06-gin-strict; do \
		echo "==> $$v"; \
		mkdir -p $$v/gen; \
		oapi-codegen --config $$v/oapi-codegen.yaml spec/openapi.yaml; \
	done

.PHONY: tidy
tidy: ## Run go mod tidy for all modules
	@for v in $(VARIANTS) shared; do \
		echo "==> $$v"; \
		cd $$v && go mod tidy && cd ..; \
	done

# ── Run ───────────────────────────────────────────────────────────────────

.PHONY: run
run: build ## Start all 6 servers in background
	@echo "Starting all 6 variants..."
	@cd 01-echo-standard && ./server &
	@cd 02-echo-strict    && ./server &
	@cd 03-chi-standard   && ./server &
	@cd 04-chi-strict     && ./server &
	@cd 05-gin-standard   && ./server &
	@cd 06-gin-strict     && ./server &
	@sleep 1
	@echo "All servers running:"
	@echo "  01-echo-standard  :9001"
	@echo "  02-echo-strict    :9002"
	@echo "  03-chi-standard   :9003"
	@echo "  04-chi-strict     :9004"
	@echo "  05-gin-standard   :9005"
	@echo "  06-gin-strict     :9006"

.PHONY: stop
stop: ## Kill all running servers
	@pkill -f "01-echo-standard/server" 2>/dev/null || true
	@pkill -f "02-echo-strict/server"   2>/dev/null || true
	@pkill -f "03-chi-standard/server"  2>/dev/null || true
	@pkill -f "04-chi-strict/server"    2>/dev/null || true
	@pkill -f "05-gin-standard/server"  2>/dev/null || true
	@pkill -f "06-gin-strict/server"    2>/dev/null || true
	@echo "All servers stopped."

# ── k6 tests ──────────────────────────────────────────────────────────────

.PHONY: test-errors
test-errors: ## Run error handler scenario tests (requires servers running)
	k6 run k6/error-scenarios.js

.PHONY: test-load
test-load: ## Run load test across all 6 variants (requires servers running)
	k6 run k6/load-test.js

.PHONY: test-all
test-all: run ## Start servers then run both test suites
	@sleep 1
	$(MAKE) test-errors
	$(MAKE) test-load
	$(MAKE) stop

# ── Smoke test ────────────────────────────────────────────────────────────

.PHONY: smoke
smoke: ## Quick smoke test — POST + GET on all 6 variants
	@for port in 9001 9002 9003 9004 9005 9006; do \
		echo "==> :$$port"; \
		curl -s -X POST http://localhost:$$port/compute \
			-H "Content-Type: application/json" \
			-d '{"name":"smoke-test","tenantId":"t1","provider":"gcp"}' | python3 -m json.tool --no-ensure-ascii; \
		curl -s http://localhost:$$port/compute/smoke-test | python3 -m json.tool --no-ensure-ascii; \
		echo; \
	done

.PHONY: smoke-errors
smoke-errors: ## Quick smoke test for error responses — verify JSON shape
	@for port in 9001 9002 9003 9004 9005 9006; do \
		echo "==> :$$port — missing name (expect 400)"; \
		curl -s -X POST http://localhost:$$port/compute \
			-H "Content-Type: application/json" \
			-d '{"tenantId":"t1","provider":"gcp"}' | python3 -m json.tool --no-ensure-ascii; \
		echo "==> :$$port — not found (expect 404)"; \
		curl -s http://localhost:$$port/compute/does-not-exist | python3 -m json.tool --no-ensure-ascii; \
		echo; \
	done

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
