prefix=/usr/local
bindir=$(prefix)/bin

ifneq (, $(BUILDX_BIN))
	export BUILDX_CMD = $(BUILDX_BIN)
else ifneq (, $(shell docker buildx version))
	export BUILDX_CMD = docker buildx
else ifneq (, $(shell command -v buildx))
	export BUILDX_CMD = $(command -v buildx)
endif

export BUILDX_CMD ?= docker buildx

.PHONY: binaries
binaries:
	$(BUILDX_CMD) bake binaries

.PHONY: cross
cross:
	$(BUILDX_CMD) bake binaries-cross

.PHONY: images
images:
# moby/buildkit:local and moby/buildkit:local-rootless are created on Docker
	hack/images local moby/buildkit
	TARGET=rootless hack/images local moby/buildkit

.PHONY: install
install:
	mkdir -p $(DESTDIR)$(bindir)
	install bin/build/* $(DESTDIR)$(bindir)

.PHONY: release
release:
	./hack/release

.PHONY: clean
clean:
	rm -rf ./bin

.PHONY: test
test:
	./hack/test integration gateway dockerfile

.PHONY: test-race
test-race:
	CGO_ENABLED=1 GOBUILDFLAGS="-race" ./hack/test integration gateway dockerfile

.PHONY: lint
lint:
	$(BUILDX_CMD) bake lint

.PHONY: validate-vendor
validate-vendor:
	$(BUILDX_CMD) bake validate-vendor

.PHONY: validate-shfmt
validate-shfmt:
	$(BUILDX_CMD) bake validate-shfmt

.PHONY: shfmt
shfmt:
	$(BUILDX_CMD) bake shfmt

.PHONY: validate-authors
validate-authors:
	$(BUILDX_CMD) bake validate-authors

.PHONY: validate-generated-files
validate-generated-files:
	$(BUILDX_CMD) bake validate-generated-files

.PHONY: validate-archutil
validate-archutil:
	$(BUILDX_CMD) bake validate-archutil

.PHONY: validate-doctoc
validate-doctoc:
	$(BUILDX_CMD) bake validate-doctoc

.PHONY: validate-docs
validate-docs:
	$(BUILDX_CMD) bake validate-docs

.PHONY: validate-all
validate-all: test lint validate-vendor validate-generated-files validate-archutil validate-doctoc validate-docs

.PHONY: vendor
vendor:
	$(eval $@_TMP_OUT := $(shell mktemp -d -t buildkit-output.XXXXXXXXXX))
	$(BUILDX_CMD) bake --set "*.output=type=local,dest=$($@_TMP_OUT)" vendor
	rm -rf ./vendor
	cp -R "$($@_TMP_OUT)"/out/* .
	rm -rf "$($@_TMP_OUT)"/

.PHONY: generated-files
generated-files:
	$(BUILDX_CMD) bake generated-files

.PHONY: archutil
archutil:
	$(BUILDX_CMD) bake archutil

.PHONY: authors
authors:
	$(BUILDX_CMD) bake authors

.PHONY: doctoc
doctoc:
	$(BUILDX_CMD) bake doctoc

.PHONY: docs
docs:
	$(BUILDX_CMD) bake docs

.PHONY: mod-outdated
mod-outdated:
	$(BUILDX_CMD) bake mod-outdated
