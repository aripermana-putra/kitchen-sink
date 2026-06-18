APPS := 01-prometheus-direct 02-otel-sdk 03-hashicorp 04-victoriametrics

# ── Local development with docker-compose ─────────────────────────────────

.PHONY: up
up: ## Start all apps + OTel Collector
	docker compose -f deploy/docker-compose.yml up --build -d

.PHONY: down
down: ## Stop and remove all containers
	docker compose -f deploy/docker-compose.yml down

.PHONY: logs
logs: ## Tail logs for all services
	docker compose -f deploy/docker-compose.yml logs -f

# ── Go tidy for each module ───────────────────────────────────────────────

.PHONY: tidy
tidy: ## Run go mod tidy in all modules
	for app in $(APPS); do \
		echo "==> $$app"; \
		cd $$app && go mod tidy && cd ..; \
	done

# ── Load test ─────────────────────────────────────────────────────────────

.PHONY: load
load: ## Send 500 POST requests to each app (2000 total, in parallel)
	@echo "Sending 500 requests to each app in parallel..."
	@{ \
		for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8081/workflow/submit; done & \
		for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8082/workflow/submit; done & \
		for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8083/workflow/submit; done & \
		for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8084/workflow/submit; done & \
		wait; \
	}
	@echo "Done."

.PHONY: load-01
load-01: ## Send 500 requests to 01-prometheus-direct only
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8081/workflow/submit; done

.PHONY: load-02
load-02: ## Send 500 requests to 02-otel-sdk only
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8082/workflow/submit; done

.PHONY: load-03
load-03: ## Send 500 requests to 03-hashicorp only
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8083/workflow/submit; done

.PHONY: load-04
load-04: ## Send 500 requests to 04-victoriametrics only
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8084/workflow/submit; done

# ── Quick metrics peek ────────────────────────────────────────────────────

.PHONY: metrics-01
metrics-01: ## Show raw metrics from 01-prometheus-direct
	@curl -s http://localhost:8081/metrics | grep -A2 "workflow_submission_duration"

.PHONY: metrics-02
metrics-02: ## Show raw metrics from 02-otel-sdk (via OTel Collector)
	@curl -s http://localhost:8889/metrics | grep -A2 "workflow_submission_duration"

.PHONY: metrics-03
metrics-03: ## Show raw metrics from 03-hashicorp
	@curl -s http://localhost:8083/metrics | grep -A2 "workflow_submission_duration"

.PHONY: metrics-04
metrics-04: ## Show raw metrics from 04-victoriametrics
	@curl -s http://localhost:8084/metrics | grep -A2 "workflow_submission_duration"

# ── Prometheus scrape config (applied to existing K8s Prometheus) ─────────

.PHONY: scrape-apply
scrape-apply: ## Apply additionalScrapeConfigs to existing K8s Prometheus
	kubectl apply -f deploy/k8s/additional-scrape-configs.yaml

.PHONY: scrape-delete
scrape-delete: ## Remove additionalScrapeConfigs from K8s Prometheus
	kubectl delete secret kitchen-sink-scrape-configs -n monitoring

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
