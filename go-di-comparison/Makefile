VARIANTS := 01-manual 02-uber-fx 03-wire

.PHONY: generate
generate: ## Regenerate oapi-codegen for all variants + wire for 03
	@for v in $(VARIANTS); do \
		echo "==> $$v: oapi-codegen"; \
		mkdir -p $$v/gen; \
		oapi-codegen --config $$v/oapi-codegen.yaml spec/openapi.yaml; \
	done
	@echo "==> 03-wire: wire gen"
	@cd 03-wire && wire gen ./cmd/api-server/

.PHONY: build
build: generate ## Build all 3 variants
	@for v in $(VARIANTS); do \
		echo "==> $$v"; \
		cd $$v && go build ./... && cd ..; \
	done

.PHONY: tidy
tidy: ## Run go mod tidy for all modules
	@for v in $(VARIANTS) shared; do \
		echo "==> $$v"; \
		cd $$v && go mod tidy && cd ..; \
	done

.PHONY: run-manual
run-manual: generate ## Run 01-manual on :8081
	cd 01-manual && go run ./cmd/api-server/

.PHONY: run-fx
run-fx: generate ## Run 02-uber-fx on :8082
	cd 02-uber-fx && go run ./cmd/api-server/

.PHONY: run-wire
run-wire: generate ## Run 03-wire on :8083
	cd 03-wire && go run ./cmd/api-server/

.PHONY: wiring-lines
wiring-lines: ## Count wiring lines in each variant
	@echo "=== Wiring line count (WIRE markers) ==="
	@echo -n "01-manual: " && grep -c "WIRE" 01-manual/cmd/api-server/main.go
	@echo -n "02-uber-fx: " && grep -c "WIRE" 02-uber-fx/cmd/api-server/main.go
	@echo -n "03-wire: " && grep -c "WIRE" 03-wire/cmd/api-server/wire.go

.PHONY: scenario-c
scenario-c: ## Demonstrate error detection — remove TemporalClient from wiring
	@echo "=== 01-manual: compile-time detection ==="
	@cp 01-manual/cmd/api-server/main.go /tmp/main_01_backup.go
	@sed -i '' 's/compute.NewService(k8s, temporal)/compute.NewService(k8s)/' 01-manual/cmd/api-server/main.go
	@cd 01-manual && go build ./cmd/api-server/ 2>&1 | head -3 || true
	@cp /tmp/main_01_backup.go 01-manual/cmd/api-server/main.go
	@echo ""
	@echo "=== 02-uber-fx: runtime detection (build succeeds) ==="
	@cp 02-uber-fx/cmd/api-server/main.go /tmp/main_02_backup.go
	@sed -i '' '/fx.Provide(platform.NewTemporalClient)/d' 02-uber-fx/cmd/api-server/main.go
	@cd 02-uber-fx && go build -o /tmp/fx-broken ./cmd/api-server/ && /tmp/fx-broken 2>&1 | grep "ERROR\|missing" | head -3 || true
	@cp /tmp/main_02_backup.go 02-uber-fx/cmd/api-server/main.go
	@echo ""
	@echo "=== 03-wire: wire-generate-time detection ==="
	@cp 03-wire/cmd/api-server/wire.go /tmp/wire_backup.go
	@sed -i '' '/platform.NewTemporalClient/d' 03-wire/cmd/api-server/wire.go
	@cd 03-wire && wire gen ./cmd/api-server/ 2>&1 | head -5 || true
	@cp /tmp/wire_backup.go 03-wire/cmd/api-server/wire.go
	@cd 03-wire && wire gen ./cmd/api-server/ > /dev/null 2>&1

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
