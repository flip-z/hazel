.PHONY: build test

build:
	go build -o bin/hazel ./cmd/hazel

test:
	go test ./...
