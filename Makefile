DATABASE_URL ?= postgres://poker:poker@localhost:5432/poker?sslmode=disable
# Для тестов нужна роль с правом CREATE DATABASE: каждый тест создаёт и удаляет свою БД.
TEST_DATABASE_URL ?= postgres://poker:poker@localhost:5432/postgres?sslmode=disable

.PHONY: up down db run test build reset

up:          ## БД + сервер в Docker, Swagger: http://localhost:8080/swagger/
	docker compose up --build -d

down:
	docker compose down

db:          ## только PostgreSQL в Docker (для локального `make run`)
	docker compose up -d db

run:         ## сервер локально, миграции применяются при старте
	DATABASE_URL="$(DATABASE_URL)" go run ./cmd/server

test:        ## интеграционные тесты API на временной БД
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -count=1 ./...

build:
	go build -o bin/server ./cmd/server

reset:       ## удалить данные БД (volume)
	docker compose down -v
