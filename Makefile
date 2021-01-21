.PHONY: all
all: dagger

.PHONY: generate
generate:
	@go generate ./dagger

.PHONY: dagger
dagger: generate
	go build -o ./cmd/dagger/ ./cmd/dagger/

.PHONY: test
test:
	go test -v ./...

.PHONY: cuefmt
cuefmt:
	@(cue fmt -s ./... && cue trim -s ./...)

.PHONY: lint
lint: generate cuefmt
	golangci-lint run
	@test -z "$$(git status -s . | grep -e "^ M"  | grep .cue | cut -d ' ' -f3 | tee /dev/stderr)"
	@test -z "$$(git status -s . | grep -e "^ M"  | grep gen.go | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: integration
integration: dagger
	# Self-diagnostics
	./examples/tests/test-test.sh 2>/dev/null
	# Actual integration tests
	./examples/tests/test.sh all
