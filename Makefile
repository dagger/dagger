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
	@awk -F':+ |$(HELP_TARGET_DEPTH)' '/^[0-9a-zA-Z._%-\/~]+:+.+$(HELP_TARGET_DEPTH).+$$/ { printf "$(GREEN)%-30s\033[0m %s\n", $$1, $$3 }' $(MAKEFILE_LIST) | sort
	@echo

.PHONY: dagger
dagger: # Build a dev dagger binary
	CGO_ENABLED=0 go build -o ./cmd/dagger/dagger-cue -ldflags '-s -w' ./cmd/dagger/

.PHONY: dagger-debug
dagger-debug: # Build a debug version of the dev dagger binary
	go build -race -o ./cmd/dagger/dagger-debug ./cmd/dagger/

.PHONY: install
install: # Install a dev dagger binary
	go install ./cmd/dagger

.PHONY: test
test: dagger # Run all tests
	./cmd/dagger/dagger do test unit

.PHONY: golint
golint: dagger # Go lint
	./cmd/dagger/dagger do lint go

.PHONY: cuefmt
cuefmt: # Format all cue files
	find . -name '*.cue' -not -path '*/cue.mod/*' -print | time xargs -n 1 -P 8 cue fmt -s

.PHONY: cuelint
cuelint: dagger # Lint all cue files
	./cmd/dagger/dagger do lint cue

.PHONY: shellcheck
shellcheck: dagger # Run shellcheck
	./cmd/dagger/dagger do lint shell

.PHONY: lint
lint: dagger # Lint everything
	./cmd/dagger/dagger do lint

.PHONY: integration
integration: core-integration universe-test doc-test # Run all integration tests

.PHONY: core-integration
core-integration: dagger # Run core integration tests
	./cmd/dagger/dagger do test integration core

.PHONY: universe-test
universe-test: dagger # Run universe tests
	./cmd/dagger/dagger do test integration universe

.PHONY: experimental-universe-tests
experimental-universe-tests: dagger # Run experimental universe tests
	./cmd/dagger/dagger do test integration experimental

.PHONY: doc-test
doc-test: dagger # Test docs
	./cmd/dagger/dagger do test integration doc

.PHONY: docs
docs: dagger # Generate docs
	DAGGER_TELEMETRY_DISABLE=1 ./cmd/dagger/dagger doc --output ./docs/reference --format md

.PHONY: mdlint
mdlint: # Markdown lint for web
	@markdownlint ./docs README.md

.PHONY: web_redirects
web_redirects:
	# Generate netlify _redirects
	find ./docs/ -path "*/*-*.md*" | xargs grep -h slug | cut -d ' ' -f2 | awk -F '/' 'NF>2{print "/"$$2"/*",$$0,307}' | sort -t '/' -k 2 > ./website/static/_redirects

.PHONY: web
web: web_redirects # Run the website locally
	yarn --cwd "./website" install
	yarn --cwd "./website" start

.PHONY: todo
todo: # Find all TODO items
	grep -r -A 1 "TODO:" $(CURDIR)

KIND_CLUSTER_NAME ?= kind-dagger
.PHONY: kind
kind: # Create Kind cluster
	@echo "TODO: Maybe convert to a Dagger package: https://github.com/kubernetes-sigs/kind/issues/2833"
	(  kind get clusters | grep -q $(KIND_CLUSTER_NAME) ) \
	|| kind create cluster --name $(KIND_CLUSTER_NAME) --config kind-dagger.yml

define extract_correct_cue_version_from_go_mod
$$(awk -F'[v-]' '/cue/ { print $$2 }' < $(CURDIR)/go.mod | tail -n 1)
endef
.PHONY: _cue_version
_cue_version:
	  echo "v$(extract_correct_cue_version_from_go_mod)"

~/go/bin/cue: # Install the right CUE version
	go install cuelang.org/go/cmd/cue@v$(extract_correct_cue_version_from_go_mod)
