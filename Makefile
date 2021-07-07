.PHONY: all
all: dagger

.PHONY: dagger
dagger:
	CGO_ENABLED=0 go build -o ./cmd/dagger/ -ldflags '-s -w' ./cmd/dagger/

.PHONY: dagger-debug
dagger-debug:
	go build -race -o ./cmd/dagger/dagger-debug ./cmd/dagger/

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
lint: shellcheck cuelint golint check-buildkit-version docslint

.PHONY: check-buildkit-version
check-buildkit-version:
	@test \
		"$(shell grep buildkit ./go.mod | cut -d' ' -f2)" = \
		"$(shell grep ' = "v' ./util/buildkitd/buildkitd.go | sed -E 's/^.*version.*=.*\"(v.*)\"/\1/' )" \
		|| { echo buildkit version mismatch go.mod != util/buildkitd/buildkitd.go ; exit 1; }

.PHONY: integration
integration: core-integration universe-test doc-test

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
	DAGGER_BINARY="../../../cmd/dagger/dagger-debug" yarn --cwd "./docs/learn/tests" test

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
