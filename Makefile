.PHONY: web build image test

VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
TESTFLAGS ?=
TEST_DB ?= postgres://tollgate:tollgate@localhost:5432/tollgate?sslmode=disable

web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o tollgate ./cmd/tollgate

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t tollgate:$(VERSION) .

# DB-backed tests skip silently without TOLLGATE_TEST_DATABASE_URL; `make test` always sets it,
# so a missing Postgres fails loudly. Start it with: docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d postgres redis
# -timeout 20m: the DB-backed packages (console/api, core/db/repo) build a fresh
# schema + migrations + bcrypt-hashed users per test, serially, and under -race on
# the 2-core CI runner console/api alone ran ~600s — past go test's 10m default.
# 20m bounds the run without masking a real deadlock (which hangs regardless).
test:
	TOLLGATE_TEST_DATABASE_URL='$(TEST_DB)' go test -count=1 -timeout 20m $(TESTFLAGS) ./...
