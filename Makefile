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
	@(cue fmt -s ./...)

.PHONY: lint
lint: cuefmt
	golangci-lint run
	@test -z "$$(git status -s . | grep -e "^ M"  | grep .cue | cut -d ' ' -f3 | tee /dev/stderr)"
	@test -z "$$(git status -s . | grep -e "^ M"  | grep gen.go | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: integration
integration: dagger-debug
	# Self-diagnostics
	./tests/test-test.sh 2>/dev/null
	# Actual integration tests
	DAGGER_BINARY="./cmd/dagger/dagger-debug" time ./tests/test.sh all

update-examples:
	rsync -avH --delete ./stdlib/cue.mod/pkg/ ./examples/*/cue.mod/pkg/
