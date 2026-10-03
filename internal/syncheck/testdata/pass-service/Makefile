setup:
	pre-commit install --install-hooks

up:
	@if [ ! -f .env ]; then \
        cp .env.example .env; \
    fi
	docker compose up -d

down:
	docker compose down

fresh:
	@if [ ! -f .env ]; then \
        cp .env.example .env; \
    fi
	docker compose down --remove-orphans
	docker compose build --no-cache
	docker compose up -d --build -V

full-fresh:
	@if [ ! -f .env ]; then \
        cp .env.example .env; \
    fi
	docker compose down --remove-orphans -v
	docker compose build --no-cache
	docker compose up -d --build -V

logs:
	docker compose logs -f

test:
	CGO_ENABLED=0 go test -v -race -cover -count=1 -failfast ./...

lint:
	scripts/golangci-lint.sh run --timeout=5m

# Pinned to the version promy-github-workflows' go-coverage.yml runs in CI; bump both together.
install-go-test-coverage:
	go install github.com/vladopajic/go-test-coverage/v2@v2.19.0

check-coverage: install-go-test-coverage
	go test -race -failfast -coverpkg=./... -covermode=atomic -coverprofile=coverage.out -count=1 ./...
	$$(go env GOPATH)/bin/go-test-coverage --config=./.testcoverage.yml

rename:
	find . -type f ! -path "./.git/*" ! -path "./build/*" ! -path "./Makefile" -exec sed -i.bak -e "s|github.com/ankorstore/yokai-http-template|github.com/$(to)|g" {} \;
	find . -type f -name "*.bak" -delete

# Git workflow helpers
# Git workflow helpers
git-status:
	@echo "=== Git Status with Branch Info ==="
	@git status -sb
	@echo "\n=== Uncommitted Files by Domain ==="
	@git status --short | grep -E "^ M|^M |^A |^ A" | awk '{print $$2}' | grep "internal/domain/" | sed 's|internal/domain/\([^/]*\)/.*|\1|' | sort -u | sed 's/^/  - /'

git-log:
	@git log --oneline --graph --decorate -20

git-diff:
	@echo "=== Staged Changes ==="
	@git diff --cached --stat
	@echo "\n=== Unstaged Changes ==="
	@git diff --stat

git-check:
	@echo "=== Pre-Commit Checklist ==="
	@echo "[ ] One domain/layer per commit?"
	@echo "[ ] Message follows Conventional Commits?"
	@echo "[ ] Files related to same feature?"
	@echo "[ ] < 10 files, < 500 lines?"
	@echo ""
	@echo "=== Running Tests ==="
	@go test ./... -short
	@echo ""
	@echo "=== Running Linter ==="
	@scripts/golangci-lint.sh run --timeout=5m
