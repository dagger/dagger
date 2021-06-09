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

.PHONY: lint
lint: shellcheck cuelint golint check-buildkit-version universelint

.PHONY: check-buildkit-version
check-buildkit-version:
	@test \
		"$(shell grep buildkit ./go.mod | cut -d' ' -f2)" = \
		"$(shell grep ' = "v' ./util/buildkitd/buildkitd.go | sed -E 's/^.*version.*=.*\"(v.*)\"/\1/' )" \
		|| { echo buildkit version mismatch go.mod != util/buildkitd/buildkitd.go ; exit 1; }

.PHONY: integration
integration: dagger-debug
	$(shell command -v sops > /dev/null || { echo "You need sops. On macOS: brew install sops"; exit 1; })
	$(shell command -v parallel > /dev/null || { echo "You need gnu parallel. On macOS: brew install parallel"; exit 1; })
	yarn --cwd "./tests" install
	DAGGER_BINARY="../cmd/dagger/dagger-debug" yarn --cwd "./tests" test

.PHONY: install
install: dagger
	go install ./cmd/dagger

.PHONY: universe
universe: dagger
	./cmd/dagger/dagger doc --output ./docs/reference/universe --format md

.PHONY: universelint
universelint: universe
	@test -z "$$(git status -s . | grep -e "^ M"  | grep docs/reference/universe | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: docs
docs:
	yarn --cwd "./website" install
	yarn --cwd "./website" start
