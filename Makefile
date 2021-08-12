GIT_REVISION := $(shell git rev-parse --short HEAD)

.PHONY: all
all: dagger

.PHONY: dagger
dagger:
	CGO_ENABLED=0 go build -o ./cmd/dagger/ -ldflags '-s -w -X go.dagger.io/dagger/version.Revision=$(GIT_REVISION)' ./cmd/dagger/

.PHONY: dagger-debug
dagger-debug:
	go build -race -o ./cmd/dagger/dagger-debug -ldflags '-X go.dagger.io/dagger/version.Revision=$(GIT_REVISION)' ./cmd/dagger/

.PHONY: test
test:
	go test -race -v ./...

.PHONY: golint
golint:
	golangci-lint run --timeout 3m

.PHONY: cuefmt
cuefmt:
	@(find . -name '*.cue' -exec cue fmt -s {} \;)

.PHONY: cuelint
cuelint: cuefmt
	@test -z "$$(git status -s . | grep -e "^ M"  | grep .cue | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: shellcheck
shellcheck:
	shellcheck ./tests/*.bats ./tests/*.bash
	shellcheck ./universe/*.bats ./universe/*.bash

.PHONY: lint
lint: shellcheck cuelint golint docslint

.PHONY: integration
integration: core-integration universe-test

.PHONY: core-integration
core-integration: dagger-debug
	yarn --cwd "./tests" install
	DAGGER_BINARY="../cmd/dagger/dagger-debug" yarn --cwd "./tests" test

.PHONY: universe-test
universe-test: dagger-debug
	yarn --cwd "./universe" install
	DAGGER_BINARY="../cmd/dagger/dagger-debug" yarn --cwd "./universe" test

.PHONY: doc-test
doc-test: dagger-debug
	yarn --cwd "./docs/learn/tests" install
	DAGGER_BINARY="$(shell pwd)/cmd/dagger/dagger-debug" yarn --cwd "./docs/learn/tests" test

.PHONY: install
install: dagger
	go install ./cmd/dagger

.PHONY: docs
docs: dagger
	./cmd/dagger/dagger doc --output ./docs/reference/universe --format md

.PHONY: docslint
docslint: docs
	@test -z "$$(git status -s . | grep -e "^ M"  | grep docs/reference/universe | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: web
web:
	yarn --cwd "./website" install
	yarn --cwd "./website" start
