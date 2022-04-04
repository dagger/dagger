SHELL := bash# we want bash behaviour in all shell invocations

# https://stackoverflow.com/questions/4842424/list-of-ansi-color-escape-sequences
BOLD := \033[1m
NORMAL := \033[0m
GREEN := \033[1;32m

XDG_CONFIG_HOME ?= $(CURDIR)/.config
export XDG_CONFIG_HOME
.DEFAULT_GOAL := help
HELP_TARGET_DEPTH ?= \#
.PHONY: help
help: # Show how to get started & what targets are available
	@printf "This is a list of all the make targets that you can run, e.g. $(BOLD)make dagger$(NORMAL) - or $(BOLD)m dagger$(NORMAL)\n\n"
	@awk -F':+ |$(HELP_TARGET_DEPTH)' '/^[0-9a-zA-Z._%-]+:+.+$(HELP_TARGET_DEPTH).+$$/ { printf "$(GREEN)%-20s\033[0m %s\n", $$1, $$3 }' $(MAKEFILE_LIST) | sort
	@echo

GIT_REVISION := $(shell git rev-parse --short HEAD)
GO_PACKAGES := $(go list -f {{.Dir}} ./... | grep -v /vendor/)

.PHONY: dagger
dagger: # Build a dev dagger binary
	CGO_ENABLED=0 go build -o ./cmd/dagger/ -ldflags '-s -w -X go.dagger.io/dagger/version.Revision=$(GIT_REVISION)' ./cmd/dagger/

.PHONY: dagger-debug
dagger-debug: # Build a debug version of the dev dagger binary
	go build -race -o ./cmd/dagger/dagger-debug -ldflags '-X go.dagger.io/dagger/version.Revision=$(GIT_REVISION)' ./cmd/dagger/

.PHONY: install
install: # Install a dev dagger binary
	go install -ldflags '-X go.dagger.io/dagger/version.Revision=$(GIT_REVISION)' ./cmd/dagger

.PHONY: test
test: # Run all tests
	go test -race -v ./...

.PHONY: golint
golint: # Go lint
	golangci-lint run --timeout 3m

.PHONY: fmt
fmt: # Format code
	go fmt $(GO_PACKAGES)
	gofumpt -w $(GO_PACKAGES)

.PHONY: cuefmt
cuefmt: # Format all cue files
	find . -name '*.cue' -not -path '*/cue.mod/*' -print | time xargs -n 1 -P 8 cue fmt -s

.PHONY: cuelint
cuelint: cuefmt # Lint and format all cue files
	@test -z "$$(git status -s . | grep -e "^ M"  | grep "\.cue" | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: shellcheck
shellcheck: # Run shellcheck
	shellcheck $$(find . -type f \( -iname \*.bats -o -iname \*.bash -o -iname \*.sh \) -not -path "*/node_modules/*" -not -path "*/bats-*/*")

.PHONY: lint
lint: shellcheck cuelint golint docslint mdlint # Lint everything

.PHONY: integration
integration: core-integration universe-test doc-test # Run all integration tests

.PHONY: core-integration
core-integration: dagger-debug # Run core integration tests
	yarn --cwd "./tests" install
	DAGGER_BINARY="$(shell pwd)/cmd/dagger/dagger-debug" yarn --cwd "./tests" test

# .PHONY: universe-test
# universe-test: dagger-debug # Run universe tests
# 	yarn --cwd "./universe" install
# 	DAGGER_BINARY="$(shell pwd)/cmd/dagger/dagger-debug" yarn --cwd "./universe" test

.PHONY: universe-test
universe-test: dagger-debug # Run universe tests
	yarn --cwd "./pkg/universe.dagger.io" install
	DAGGER_BINARY="$(shell pwd)/cmd/dagger/dagger-debug" yarn --cwd "./pkg/universe.dagger.io" test

.PHONY: doc-test
doc-test: dagger-debug # Test docs
	yarn --cwd "./docs/learn/tests" install
	DAGGER_BINARY="$(shell pwd)/cmd/dagger/dagger-debug" yarn --cwd "./docs/learn/tests" test

.PHONY: docs
docs: dagger # Generate docs
	DAGGER_TELEMETRY_DISABLE=1 ./cmd/dagger/dagger doc --output ./docs/reference --format md

.PHONY: docslint
docslint: docs # Generate & lint docs
	@test -z "$$(git status -s . | grep -e "^ M"  | grep docs/reference | cut -d ' ' -f3 | tee /dev/stderr)"

.PHONY: mdlint
mdlint: # Markdown lint for web
	@markdownlint ./docs README.md

.PHONY: web
web: # Run the website locally
	yarn --cwd "./website" install
	yarn --cwd "./website" start

.PHONY: todo
todo: # Find all TODO items
	grep -r -A 1 "TODO:" $(CURDIR)
