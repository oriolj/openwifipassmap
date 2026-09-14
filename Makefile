## OpenWifiPassMap — dev & build tasks.
## Conventions: `make start` is the main local entry point; `make tmux` manages
## the project's tmux session.

SHELL := /bin/bash
TMUX_SESSION := openwifipassmap

# Backend port. 8080 is conventional but often taken locally (e.g. syncthing);
# override with `make start PORT=8744`.
PORT ?= 8080
API_BASE ?= http://localhost:$(PORT)
# Point the frontend at the deployed backend for `make start-remote`,
# and bake into the CLI for `make cli-build-prod` / `cli-release-prod`.
REMOTE_API ?= https://openwifipassmap.oriolj.com

GREEN := \033[0;32m
BLUE  := \033[0;34m
NC    := \033[0m

.PHONY: help start start-local start-remote server mobile web migrate css css-if-needed \
        build cli-build cli-build-prod cli-release cli-release-prod \
        test test-go e2e fmt vet deps \
        docker-build docker-run tmux tmux-d tmux-n tmux-new-session clean \
        prod-status slow-requests slow-queries routes error-traces trace prod-top-queries \
        logs-prod logs-worker logs-prod-grep logs-prod-errors logs-prod-5xx logs-beta prod-metrics

help: ## Show this help
	@echo -e "$(BLUE)OpenWifiPassMap$(NC) — make targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-18s$(NC) %s\n", $$1, $$2}'

# Load local secrets/config (RESEND_API_KEY, PUBLIC_BASE_URL, …) if present.
# .env.local is gitignored; missing file is fine.
LOAD_ENV := if [ -f .env.local ]; then set -a; . ./.env.local; set +a; fi

css: ## Compile Tailwind+DaisyUI and vendor leaflet into internal/web/static
	@cd web && npm install --no-audit --no-fund --silent && npm run build
	@echo -e "$(GREEN)built internal/web/static (app.css + vendor)$(NC)"

# Build only when the output is missing (fast no-op for everyday starts).
css-if-needed:
	@[ -f internal/web/static/app.css ] || $(MAKE) -s css

start: css-if-needed ## Run backend + mobile dev server (mobile → local backend)
	@echo -e "$(GREEN)Starting backend (:$(PORT)) + mobile dev (:5173)$(NC)"
	@trap 'kill 0' EXIT; \
		( $(LOAD_ENV); ADDR=:$(PORT) DEV=1 go run ./cmd/server ) & \
		( cd mobile && VITE_API_BASE=$(API_BASE) npm run dev ) & \
		wait

start-local: start ## Alias for `start` (local API)

start-remote: ## Run mobile dev server pointed at the remote/prod backend
	@echo -e "$(GREEN)Starting mobile dev (:5173) → $(REMOTE_API)$(NC)"
	@cd mobile && VITE_API_BASE=$(REMOTE_API) npm run dev

server: css-if-needed ## Run the Go backend only (API + public web)
	@$(LOAD_ENV); ADDR=:$(PORT) DEV=1 go run ./cmd/server

mobile: ## Run the Vite/React mobile dev server only
	@cd mobile && VITE_API_BASE=$(API_BASE) npm run dev

web: server ## The public web is served by the backend; alias for `server`

migrate: ## Apply schema.sql to the DB (the server also auto-migrates on boot)
	@mkdir -p data && sqlite3 data/wifispot.db < migrations/schema.sql && \
		echo -e "$(GREEN)schema applied to data/wifispot.db$(NC)"

build: ## Build server + CLI into ./bin
	@mkdir -p bin
	@go build -o bin/server ./cmd/server
	@go build -o bin/wifispot ./cmd/wifispot
	@echo -e "$(GREEN)built bin/server and bin/wifispot$(NC)"

cli-build: ## Build the wifispot CLI for the current platform (default server: localhost)
	@mkdir -p bin && go build -o bin/wifispot ./cmd/wifispot && \
		echo -e "$(GREEN)built bin/wifispot$(NC)"

cli-build-prod: ## Build the wifispot CLI with REMOTE_API baked in as the default server
	@mkdir -p bin && go build \
		-ldflags "-X main.defaultServer=$(REMOTE_API)" \
		-o bin/wifispot ./cmd/wifispot && \
		echo -e "$(GREEN)built bin/wifispot → $(REMOTE_API)$(NC)"

cli-release: ## Cross-compile the CLI for linux/macos (amd64+arm64)
	@mkdir -p dist
	@for os in linux darwin; do for arch in amd64 arm64; do \
		echo -e "$(BLUE)building $$os/$$arch$(NC)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -o dist/wifispot-$$os-$$arch ./cmd/wifispot; \
	done; done
	@echo -e "$(GREEN)CLI binaries in ./dist$(NC)"

cli-release-prod: ## Cross-compile the CLI with REMOTE_API baked in as the default server
	@mkdir -p dist
	@for os in linux darwin; do for arch in amd64 arm64; do \
		echo -e "$(BLUE)building $$os/$$arch → $(REMOTE_API)$(NC)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags "-X main.defaultServer=$(REMOTE_API)" \
			-o dist/wifispot-$$os-$$arch ./cmd/wifispot; \
	done; done
	@echo -e "$(GREEN)CLI binaries in ./dist (default server: $(REMOTE_API))$(NC)"

test: test-go e2e ## Run Go tests + Playwright e2e

test-go: ## Run Go unit tests
	@go test ./...

e2e: ## Run Playwright end-to-end tests (starts servers automatically)
	@cd e2e && npx playwright test

fmt: ## gofmt the Go code
	@gofmt -w cmd/ internal/

vet: ## go vet
	@go vet ./...

deps: ## Tidy Go deps + install mobile/e2e deps
	@go mod tidy
	@cd mobile && npm install
	@cd e2e && npm install

docker-build: ## Build the deploy image (SOURCE_COMMIT baked in as the release id)
	@docker build -f docker/Dockerfile --build-arg SOURCE_COMMIT=$$(git rev-parse --short=12 HEAD) -t openwifipassmap:latest .

docker-run: ## Run the deploy image locally on :$(PORT) (file-replica Litestream, /metrics open via DEV=1)
	@docker run --rm -p $(PORT):8080 -e DEV=1 -v openwifipassmap-data:/data openwifipassmap:latest

## ---- Production observability (estate stack on monitor-1-nc; fleet-observability skill) ----
# Needs ~/.config/oj-loki/env (homelab/ansible --tags loki-logs) and the oj-traces CLI.
# SINCE=24h by default (Tempo keeps 30 d; the log targets grep the same window); LIMIT=20;
# P=openwifipassmap. Honest state (GRAFANA_AND_METRICS.md): the Prometheus targets need
# the hub's openwifipassmap-app scrape job live; the Loki/Tempo targets need jluv-apps-1
# enrolled in the fleet (no Alloy on the host yet) — until then they answer empty.
P ?= openwifipassmap
SINCE ?= 24h
LIMIT ?= 20
PROM ?= http://monitor-1-nc:9090
HQ_SECRETS ?= $(HOME)/Syncthing/Syncthing-mobile-docs/hq/homelab/secrets

prod-status: ## One-screen PROD status from Prometheus: health/release, target, req/s, 5xx, p95, spots/users/community KPIs, SQLite, Litestream
	@scripts/prod_status.sh $(P)

slow-requests: ## Slowest traced requests on PROD, last 24h (Tempo; every request > 1 s is kept, 10 % of the rest): make slow-requests SINCE=2h
	@oj-traces slow -p $(P) --since $(SINCE) -n $(LIMIT)

slow-queries: ## Slowest SQL spans on PROD with the statement text (Tempo, db.system=sqlite): make slow-queries SINCE=2h MIN=50ms
	@oj-traces sql -p $(P) --since $(SINCE) -n $(LIMIT) --db sqlite $(if $(MIN),--min $(MIN),)

routes: ## p50 / p95 / rate per HTTP route from traces (TraceQL metrics)
	@oj-traces routes -p $(P) --since $(SINCE)

error-traces: ## Traces that ended in an error (every one is kept by the agent)
	@oj-traces errors -p $(P) --since $(SINCE) -n $(LIMIT)

trace: ## Print one trace's span tree: make trace ID=<trace id>
	@test -n "$(ID)" || { echo "usage: make trace ID=<trace id>"; exit 1; }
	@oj-traces trace $(ID)

prod-top-queries: ## Not applicable: SQLite has no pg_stat_statements — use slow-queries (Tempo) and the route histogram
	@echo -e "$(BLUE)SQLite has no cumulative statement statistics (no pg_stat_statements).$(NC)"
	@echo -e "Use $(GREEN)make slow-queries$(NC) (Tempo SQL spans, once traces are live) and $(GREEN)make prod-status$(NC) (route latency)."

logs-prod: ## Tail PROD logs from Loki (needs ~/.config/oj-loki/env — homelab/ansible --tags loki-logs)
	@set -a; . ~/.config/oj-loki/env; set +a; \
	logcli query --tail --follow '{project="$(P)", env="prod"}'

logs-worker: ## Not applicable: single-process Go service (no worker/beat) — same stream as logs-prod
	@echo -e "$(BLUE)OpenWifiPassMap has no worker process; showing the web stream.$(NC)"
	@$(MAKE) --no-print-directory logs-prod

logs-prod-grep: ## Grep PROD logs (SINCE=24h): make logs-prod-grep Q="litestream"
	@set -a; . ~/.config/oj-loki/env; set +a; \
	logcli query --since $(SINCE) --limit $(LIMIT) '{project="$(P)", env="prod"} |~ "(?i)$(Q)"'

logs-prod-errors: ## level=ERROR lines on PROD in the window (Loki)
	@set -a; . ~/.config/oj-loki/env; set +a; \
	logcli query --since $(SINCE) --limit $(LIMIT) '{project="$(P)", env="prod"} |~ "level=ERROR|panic|litestream.*error"'

logs-prod-5xx: ## Not derivable from the access log (no status field yet) — reads the 5xx rate from Prometheus instead
	@curl -s -m 8 -G "$(PROM)/api/v1/query" --data-urlencode 'query=sum by (route) (increase($(P)_http_request_duration_seconds_count{project="$(P)",code=~"5.."}[$(SINCE)]))' | python3 scripts/promq.py

logs-beta: ## Not applicable: there is no beta environment
	@echo -e "$(BLUE)OpenWifiPassMap has no beta environment.$(NC)"

prod-metrics: ## Dump the raw openwifipassmap_* + litestream_* series from PROD's /metrics (bearer from hq secrets)
	@T=$$(grep '^OPENWIFIPASSMAP_METRICS_TOKEN=' $(HQ_SECRETS)/openwifipassmap.env | cut -d= -f2-); \
	OUT=$$(curl -s -m 10 -w '\n__HTTP__%{http_code}' -H "Authorization: Bearer $$T" $(REMOTE_API)/metrics); \
	CODE=$${OUT##*__HTTP__}; BODY=$$(printf '%s' "$$OUT" | sed '$$d'); \
	printf '%s\n' "$$BODY" | grep -E '^(openwifipassmap|litestream)_' || echo -e "$(BLUE)no series (HTTP $$CODE — 401 = METRICS_TOKEN not set on the app or wrong token; 404 = old build)$(NC)"

tmux: ## Attach to (or create) the project tmux session
	@tmux has-session -t $(TMUX_SESSION) 2>/dev/null && tmux attach -t $(TMUX_SESSION) || \
	tmux new-session -s $(TMUX_SESSION) -n dev \; \
		send-keys 'make server' C-m \; \
		split-window -h \; \
		send-keys 'make mobile' C-m \; \
		select-pane -t 0

tmux-d: ## Same as tmux, but detach every other client first (this terminal gets the full screen)
	@tmux new-session -A -D -s $(TMUX_SESSION)

tmux-n: ## Attach via a grouped session (independent navigation, shared windows/panes)
	@tmux has-session -t $(TMUX_SESSION) 2>/dev/null && \
		tmux new-session -t $(TMUX_SESSION) \; set-option destroy-unattached on || \
		tmux new-session -A -s $(TMUX_SESSION)

tmux-new-session: ## Old name of tmux-n
	@$(MAKE) --no-print-directory tmux-n

clean: ## Remove build artifacts and local DB
	@rm -rf bin dist data/*.db data/*.db-wal data/*.db-shm
	@echo -e "$(GREEN)cleaned$(NC)"
