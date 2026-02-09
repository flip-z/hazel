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

## Planning Mode

Hazel includes a deterministic planning helper (no AI) to help workshop a BACKLOG ticket before it is READY:

```sh
hazel plan HZ-0001
```

## Releases + Homebrew

This repo includes:

- `.github/workflows/release.yml`: runs GoReleaser on tags `v*`
- `.goreleaser.yaml`: builds darwin/linux (amd64/arm64) and publishes a Homebrew formula to a tap repo

Standard Homebrew setup is a separate tap repo named `homebrew-hazel` under your GitHub user/org.

One-time:

1. Create the tap repo: `homebrew-hazel`
2. Update `.goreleaser.yaml` and replace `TODO_OWNER` / `TODO_REPO`

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
