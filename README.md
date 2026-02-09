# Hazel

Filesystem-first project work queue with a lightweight web UI.

## Build

```sh
make build
./bin/hazel --help
```

## Install (Local)

```sh
go install ./cmd/hazel
```

Ensure your Go bin dir is on `PATH` (usually `$(go env GOPATH)/bin` or `$GOBIN`).

## Releases + Homebrew

This repo includes:

- `.github/workflows/release.yml`: runs GoReleaser on tags `v*`
- `.goreleaser.yaml`: builds darwin/linux (amd64/arm64) and publishes a Homebrew formula to a tap repo

Standard Homebrew setup is a separate tap repo named `homebrew-hazel` under your GitHub user/org.

One-time:

1. Create the tap repo: `homebrew-hazel`
2. Ensure `.goreleaser.yaml` points at your repos (this repo: `flip-z/hazel`, tap: `flip-z/homebrew-hazel`)

Release:

```sh
git tag v0.1.0
git push --tags
```

Users install:

```sh
brew tap flip-z/hazel
brew install hazel-cli
```
