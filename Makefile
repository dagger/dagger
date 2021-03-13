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

.PHONY: cuefmt
cuefmt:
	@(cue fmt -s ./stdlib/...)
	@(cue fmt -s ./examples/*)
	@(cue fmt -s ./tests/...)

.PHONY: lint
lint: cuefmt check-buildkit-version
	golangci-lint run
	@test -z "$$(git status -s . | grep -e "^ M"  | grep .cue | cut -d ' ' -f3 | tee /dev/stderr)"

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
	# Actual integration tests
	DAGGER_BINARY="./cmd/dagger/dagger-debug" time ./tests/test.sh all

