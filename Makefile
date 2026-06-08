APP_NAME=llm-platform

.PHONY: run
run:
	go run ./cmd/api

.PHONY: build
build:
	mkdir -p bin
	go build -o bin/api ./cmd/api

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	go fmt ./...
