.PHONY: setup
setup:
	pre-commit install --install-hooks

.PHONY: test
test:
	@echo "Running tests..."
	go test -v -race -cover ./...

.PHONY: test-integration
test-integration:
	@echo "Running integration tests..."
	@docker-compose up -d redis
	@sleep 2
	@REDIS_TEST_DSN=redis://localhost:6389/15 go test -v -race -cover ./...
	@docker-compose down

.PHONY: test-short
test-short:
	@echo "Running short tests (excluding integration)..."
	go test -v -race -cover -short ./...

.PHONY: coverage
coverage:
	@echo "Generating coverage report..."
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Pinned to the version promy-github-workflows' go-coverage.yml runs in CI; bump both together.
.PHONY: install-go-test-coverage
install-go-test-coverage:
	go install github.com/vladopajic/go-test-coverage/v2@v2.19.0

.PHONY: check-coverage
check-coverage: install-go-test-coverage
	go test -race -failfast -coverpkg=./... -covermode=atomic -coverprofile=coverage.out -count=1 ./...
	$$(go env GOPATH)/bin/go-test-coverage --config=./.testcoverage.yml

.PHONY: up
up:
	@echo "Starting Redis..."
	docker-compose up -d
	@echo "Redis is now running"

.PHONY: down
down:
	@echo "Stopping Docker services..."
	docker-compose down -v

.PHONY: logs
logs:
	docker-compose logs -f

.PHONY: lint
lint:
	@echo "Running linter..."
	scripts/golangci-lint.sh run --timeout=5m

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...

.PHONY: vet
vet:
	@echo "Running go vet..."
	go vet ./...

.PHONY: tidy
tidy:
	@echo "Tidying go.mod..."
	go mod tidy

.PHONY: clean
clean:
	@echo "Cleaning..."
	rm -rf vendor/
	rm -f coverage.out coverage.html
	go clean -cache -testcache

.PHONY: example-publisher
example-publisher:
	@echo "Running publisher example..."
	go run examples/publisher/main.go

.PHONY: example-subscriber
example-subscriber:
	@echo "Running subscriber example..."
	go run examples/subscriber/main.go

.PHONY: dlq-replay
dlq-replay:
	@echo "Replaying DLQ entries..."
	go run cmd/dlq/main.go replay \
		$(if $(stream),-stream $(stream)) \
		$(if $(type),-type $(type)) \
		$(if $(id),-id $(id)) \
		$(if $(filter $(all),true),-all) \
		$(if $(filter $(dry-run),true),-dry-run) \
		$(if $(redis),-redis $(redis)) \
		$(if $(limit),-limit $(limit))

.PHONY: dlq-inspect
dlq-inspect:
	@echo "Inspecting DLQ..."
	go run cmd/dlq/main.go inspect \
		$(if $(redis),-redis $(redis)) \
		$(if $(limit),-limit $(limit))

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  test                 - Run all tests"
	@echo "  test-integration     - Run tests with Redis via Docker"
	@echo "  test-short          - Run tests excluding integration tests"
	@echo "  coverage            - Generate test coverage report"
	@echo "  up                  - Start Redis via Docker Compose"
	@echo "  down                - Stop Docker services"
	@echo "  logs                - View Docker logs"
	@echo "  lint                - Run golangci-lint"
	@echo "  fmt                 - Format code with go fmt"
	@echo "  vet                 - Run go vet"
	@echo "  tidy                - Tidy go.mod"
	@echo "  clean               - Clean build artifacts and cache"
	@echo "  example-publisher   - Run publisher example"
	@echo "  example-subscriber  - Run subscriber example"
	@echo "  dlq-replay          - Replay DLQ entries (stream=, type=, id=, all=true, dry-run=true)"
	@echo "  dlq-inspect         - Show DLQ statistics"
	@echo "  git-status          - View git status with component grouping"
	@echo "  git-log             - View recent commit history"
	@echo "  git-diff            - View staged vs unstaged changes"
	@echo "  git-check           - Run pre-commit validation"
	@echo "  help                - Show this help message"

# Git workflow helpers
.PHONY: git-status
git-status:
	@echo "=== Git Status with Branch Info ==="
	@git status -sb
	@echo "\n=== Uncommitted Files by Component ==="
	@git status --short | grep -E "^ M|^M |^A |^ A" | awk '{print $$2}' | \
		sed -E 's|^(publisher|subscriber|event|config|errors)\.go|\1|; s|^redis/(.*)|\1 (redis)|; s|^events/(.*)|\1 (events)|; s|^examples/(.*)|\1 (examples)|; s|^testutil/(.*)|\1 (testutil)|' | \
		sort -u | sed 's/^/  - /' || echo "  (no uncommitted files)"

.PHONY: git-log
git-log:
	@git log --oneline --graph --decorate -20

.PHONY: git-diff
git-diff:
	@echo "=== Staged Changes ==="
	@git diff --cached --stat
	@echo "\n=== Unstaged Changes ==="
	@git diff --stat

.PHONY: git-check
git-check:
	@echo "=== Pre-Commit Checklist ==="
	@echo "[ ] One component/concern per commit?"
	@echo "[ ] Message follows Conventional Commits?"
	@echo "[ ] Files related to same feature?"
	@echo "[ ] < 10 files, < 500 lines?"
	@echo ""
	@echo "=== Running Tests ==="
	@go test ./... -short
	@echo ""
	@echo "=== Running Linter ==="
	@scripts/golangci-lint.sh run --timeout=5m
