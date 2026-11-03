.PHONY: test fmt fmt-check build build-agent up
test: fmt-check
	go vet ./... && go test ./...
fmt:
	gofmt -w .
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "not gofmt-formatted:"; echo "$$out"; exit 1; fi
build:
	go build -o bin/kluard ./cmd/kluard
	go build -o bin/kluard-agent ./cmd/kluard-agent
build-agent:
	go build -o bin/kluard-agent ./cmd/kluard-agent
up:
	docker compose -f deploy/docker-compose.yml up --build

.PHONY: dev
dev:
	./dev.sh
