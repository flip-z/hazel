.PHONY: build test
.PHONY: release-snapshot

build:
	go build -o bin/hazel ./cmd/hazel

test:
	go test ./...

release-snapshot:
	goreleaser release --snapshot --clean
