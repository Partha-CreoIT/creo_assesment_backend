.PHONY: run seed test build compose-up compose-down runtimes

run:
	go run ./cmd/server

seed:
	go run ./cmd/seed

test:
	go test ./...

build:
	go build -o bin/server ./cmd/server && go build -o bin/seed ./cmd/seed

compose-up:
	docker compose up -d

compose-down:
	docker compose down

runtimes:
	bash scripts/install-runtimes.sh
