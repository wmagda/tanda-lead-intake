.PHONY: fmt lint test run migrate-up migrate-down docker-up docker-down

fmt:
	gofmt -s -w internal/ cmd/ pkg/

lint: ## temporarily disabled — go not installed on this node
	@echo "lint skipped (Go toolchain not present on host)"

test:
	@echo "not yet implemented"

run: ## requires Go 1.23
	go run ./cmd/server

migrate-up:
	@echo "Apply supabase/migrations/v1__init.sql via Supabase SQL Editor or CLI"
	@echo "  supabase db push                       # supabase CLI"
	@echo "  or copy-paste into Supabase Dashboard → SQL Editor"

migrate-down:
	@echo "Manual rollback — no down migration in v1"

docker-up:
	docker compose up -d

docker-down:
	docker compose down
