.PHONY: all
all: dagger

.PHONY: dagger
dagger:
	go build -o ./cmd/dagger/ ./cmd/dagger/

.PHONY: dagger
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
	@(cue fmt -s ./stdlib/...)
	@(cue fmt -s ./examples/*/)
	@(cue fmt -s ./tests/...)

.PHONY: cuelint
cuelint: cuefmt
	@test -z "$$(git status -s . | grep -e "^ M"  | grep .cue | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: shellcheck
shellcheck:
	shellcheck ./tests/*.bats ./tests/*.bash

.PHONY: lint
lint: shellcheck cuelint golint check-buildkit-version

.PHONY: check-buildkit-version
check-buildkit-version:
	@test \
		"$(shell grep buildkit ./go.mod | cut -d' ' -f2)" = \
		"$(shell grep ' = "v' ./pkg/buildkitd/buildkitd.go | sed -E 's/^.*version.*=.*\"(v.*)\"/\1/' )" \
		|| { echo buildkit version mismatch go.mod != pkg/buildkitd/buildkitd.go ; exit 1; }

.PHONY: integration
integration: dagger-debug
	# Self-diagnostics
	./tests/test-test.sh 2>/dev/null
	# Bats based integration tests
	yarn --cwd "./tests" install
	DAGGER_BINARY="../cmd/dagger/dagger-debug" yarn --cwd "./tests" test
	# Old style integration tests tests
	DAGGER_BINARY="./cmd/dagger/dagger-debug" time ./tests/test.sh all

.PHONY: install
install: dagger
	go install ./cmd/dagger
