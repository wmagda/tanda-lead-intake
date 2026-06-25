.PHONY: fmt lint test run migrate-up migrate-down docker-up docker-down

fmt:
	gofmt -s -w internal/ cmd/

lint: ## temporarily disabled — go not installed on this node
	@echo "lint skipped (Go toolchain not present on host)"

test:
	go test ./...

run: ## Gmail + ingest worker (no HTTP)
	go run ./cmd/worker

deploy: ## local cron job
	go build -o worker ./cmd/worker/ && systemctl --user restart tanda-worker.service

ingest-test: ## one-shot ingest for local testing
	go run ./cmd/process-email \
	  -thread thread-test-001 -message msg-test-$$(date +%s) \
	  -from 'Jane Doe <jane@example.com>' -subject 'Private lesson?' \
	  -body 'Hi, beginner, Tuesday evening.'

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
