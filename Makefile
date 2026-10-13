.PHONY: web build test
web:
	cd web && npm ci && npm run build
build: web
	CGO_ENABLED=0 go build -o tollgate ./cmd/tollgate
test:
	go test ./...
