APPS := 01-prometheus-direct 02-otel-sdk 03-hashicorp 04-victoriametrics
IMAGES := \
	kitchen-sink/01-prometheus-direct \
	kitchen-sink/02-otel-sdk \
	kitchen-sink/03-hashicorp \
	kitchen-sink/04-victoriametrics

# ── Local development with docker-compose ─────────────────────────────────

.PHONY: up
up: ## Start all services (apps + Prometheus + OTel Collector + Grafana)
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
load: ## Send 200 POST requests spread across all 4 apps (requires curl)
	@echo "Sending requests to all 4 apps..."
	@for i in $$(seq 1 200); do \
		port=$$((8080 + ($$i % 4) + 1)); \
		curl -s -o /dev/null -X POST http://localhost:$$port/workflow/submit; \
	done
	@echo "Done. Check Prometheus at http://localhost:9090"

.PHONY: load-01
load-01: ## Send 500 requests to 01-prometheus-direct only
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8081/workflow/submit; done

.PHONY: load-03
load-03: ## Send 500 requests to 03-hashicorp only (observe Summary vs Histogram)
	@for i in $$(seq 1 500); do curl -s -o /dev/null -X POST http://localhost:8083/workflow/submit; done

# ── Quick metrics peek ────────────────────────────────────────────────────

.PHONY: metrics-01
metrics-01: ## Curl /metrics from 01-prometheus-direct and grep timing metric type
	@curl -s http://localhost:8081/metrics | grep -A2 "workflow_submission_duration"

.PHONY: metrics-03
metrics-03: ## Curl /metrics from 03-hashicorp and grep timing metric type
	@curl -s http://localhost:8083/metrics | grep -A2 "workflow_submission_duration"

# ── Colima / K8s ─────────────────────────────────────────────────────────

.PHONY: build-images
build-images: ## Build all Docker images
	docker build -t kitchen-sink/01-prometheus-direct:latest 01-prometheus-direct
	docker build -t kitchen-sink/02-otel-sdk:latest 02-otel-sdk
	docker build -t kitchen-sink/03-hashicorp:latest 03-hashicorp
	docker build -t kitchen-sink/04-victoriametrics:latest 04-victoriametrics

.PHONY: load-images
load-images: ## Load images into Colima's containerd (required for imagePullPolicy: Never)
	colima nerdctl -- image load < <(docker save kitchen-sink/01-prometheus-direct:latest)
	colima nerdctl -- image load < <(docker save kitchen-sink/02-otel-sdk:latest)
	colima nerdctl -- image load < <(docker save kitchen-sink/03-hashicorp:latest)
	colima nerdctl -- image load < <(docker save kitchen-sink/04-victoriametrics:latest)

.PHONY: k8s-up
k8s-up: build-images load-images ## Deploy to Colima K8s cluster
	kubectl apply -f deploy/k8s/namespace.yaml
	kubectl apply -f deploy/k8s/otel-collector.yaml
	kubectl apply -f deploy/k8s/apps.yaml
	kubectl apply -f deploy/k8s/prometheus.yaml
	@echo "Prometheus available at http://localhost:30090"

.PHONY: k8s-down
k8s-down: ## Tear down K8s deployment
	kubectl delete namespace metrics-lab

.PHONY: k8s-status
k8s-status: ## Show pod status in metrics-lab namespace
	kubectl get pods -n metrics-lab

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
