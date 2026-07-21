.PHONY: build run test lint vet tidy export migrate-up migrate-down up down docker-build

GOOSE_DRIVER ?= postgres
DATABASE_URL ?= postgres://tracker:tracker@localhost:5432/tracker?sslmode=disable

# При явном -o Go не дописывает .exe сам, а Windows не запустит файл без расширения.
BINARY ?= bin/parser
ifeq ($(OS),Windows_NT)
	BINARY := bin/parser.exe
endif

# Версия запинена и запускается через go run: не требует глобальной установки
# и гарантирует, что локально и в CI линтит одна и та же версия.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

build:
	go build -o $(BINARY) ./cmd/parser

run:
	go run ./cmd/parser

# Разовая выгрузка price_history в xlsx без запуска цикла мониторинга.
export:
	go run ./cmd/parser -export prices.xlsx

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

tidy:
	go mod tidy

# Миграции применяются и автоматически на старте приложения; эти цели — для ручной работы.
migrate-up:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations $(GOOSE_DRIVER) "$(DATABASE_URL)" up

migrate-down:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations $(GOOSE_DRIVER) "$(DATABASE_URL)" down

up:
	docker compose up -d --build

down:
	docker compose down

docker-build:
	docker compose build
