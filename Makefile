## OpenWifiPassMap — dev & build tasks.
## Conventions: `make start` is the main local entry point; `make tmux` manages
## the project's tmux session.

SHELL := /bin/bash
TMUX_SESSION := openwifipassmap

# Backend port. 8080 is conventional but often taken locally (e.g. syncthing);
# override with `make start PORT=8744`.
PORT ?= 8080
API_BASE ?= http://localhost:$(PORT)
# Point the frontend at the deployed backend for `make start-remote`.
REMOTE_API ?= https://openwifipassmap.example.com

GREEN := \033[0;32m
BLUE  := \033[0;34m
NC    := \033[0m

.PHONY: help start start-local start-remote server mobile web migrate \
        build cli-build cli-release test test-go e2e fmt vet deps \
        docker-build tmux tmux-new-session clean

help: ## Show this help
	@echo -e "$(BLUE)OpenWifiPassMap$(NC) — make targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-18s$(NC) %s\n", $$1, $$2}'

start: ## Run backend + mobile dev server (mobile → local backend)
	@echo -e "$(GREEN)Starting backend (:$(PORT)) + mobile dev (:5173)$(NC)"
	@trap 'kill 0' EXIT; \
		( ADDR=:$(PORT) DEV=1 go run ./cmd/server ) & \
		( cd mobile && VITE_API_BASE=$(API_BASE) npm run dev ) & \
		wait

start-local: start ## Alias for `start` (local API)

start-remote: ## Run mobile dev server pointed at the remote/prod backend
	@echo -e "$(GREEN)Starting mobile dev (:5173) → $(REMOTE_API)$(NC)"
	@cd mobile && VITE_API_BASE=$(REMOTE_API) npm run dev

server: ## Run the Go backend only (API + public web)
	@ADDR=:$(PORT) DEV=1 go run ./cmd/server

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

cli-build: ## Build the wifispot CLI for the current platform
	@mkdir -p bin && go build -o bin/wifispot ./cmd/wifispot && \
		echo -e "$(GREEN)built bin/wifispot$(NC)"

cli-release: ## Cross-compile the CLI for linux/macos (amd64+arm64)
	@mkdir -p dist
	@for os in linux darwin; do for arch in amd64 arm64; do \
		echo -e "$(BLUE)building $$os/$$arch$(NC)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -o dist/wifispot-$$os-$$arch ./cmd/wifispot; \
	done; done
	@echo -e "$(GREEN)CLI binaries in ./dist$(NC)"

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

docker-build: ## Build the deploy image
	@docker build -f docker/Dockerfile -t openwifipassmap:latest .

tmux: ## Attach to (or create) the project tmux session
	@tmux has-session -t $(TMUX_SESSION) 2>/dev/null && tmux attach -t $(TMUX_SESSION) || \
	tmux new-session -s $(TMUX_SESSION) -n dev \; \
		send-keys 'make server' C-m \; \
		split-window -h \; \
		send-keys 'make mobile' C-m \; \
		select-pane -t 0

tmux-new-session: ## Join the session via a grouped session (shared windows, own view)
	@tmux new-session -t $(TMUX_SESSION) \; set-option destroy-unattached on 2>/dev/null || $(MAKE) tmux

clean: ## Remove build artifacts and local DB
	@rm -rf bin dist data/*.db data/*.db-wal data/*.db-shm
	@echo -e "$(GREEN)cleaned$(NC)"
