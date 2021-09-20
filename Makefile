SHELL:=/bin/bash

export REPO = $(shell go list -m)
export GO111MODULE=on
export GOPROXY=https://proxy.golang.org|direct
export CGO_ENABLED = 1

export GIT_VERSION = $(shell git describe --tags --always)
export GIT_COMMIT = $(shell git rev-parse HEAD)
export GIT_COMMIT_TIME = $(shell TZ=UTC git show -s --format=%cd --date=format-local:%Y-%m-%dT%TZ)
export GIT_TREE_STATE = $(shell sh -c '(test -n "$(shell git status -s)" && echo "dirty") || echo "clean"')

GOLANGCILINT_VERSION = 1.42.1
GORELEASER_VERSION = 0.179.0

GO_BUILD_ARGS = \
  -gcflags "all=-trimpath=$(shell dirname $(shell pwd))" \
  -asmflags "all=-trimpath=$(shell dirname $(shell pwd))" \
  -ldflags " \
    -s \
    -w \
    -X '$(REPO)/internal/version.GitVersion=$(GIT_VERSION)' \
    -X '$(REPO)/internal/version.GitCommit=$(GIT_COMMIT)' \
    -X '$(REPO)/internal/version.GitCommitTime=$(GIT_COMMIT_TIME)' \
    -X '$(REPO)/internal/version.GitTreeState=$(GIT_TREE_STATE)' \
  " \

.PHONY: all
all: install

.PHONY: build
build:
	go build $(GO_BUILD_ARGS) -o bin/dcm

.PHONY: test
test:
	go test ./...

.PHONY: install
install: build
	install bin/dcm $(shell go env GOPATH)/bin

.PHONY: lint
lint:
	./scripts/fetch golangci-lint $(GOLANGCILINT_VERSION) && ./bin/golangci-lint --timeout 3m run

.PHONY: release
RELEASE_ARGS?=release --rm-dist --snapshot
release:
	./scripts/fetch goreleaser $(GORELEASER_VERSION) && ./bin/goreleaser $(RELEASE_ARGS)
