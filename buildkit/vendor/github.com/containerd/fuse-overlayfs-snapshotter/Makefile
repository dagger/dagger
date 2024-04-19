#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Deliverables path
DESTDIR ?= /usr/local
BINDIR ?= $(DESTDIR)/bin

# Tools path
ECHO ?= echo
DOCKER ?= docker
GO ?= go
MKDIR ?= mkdir
TAR ?= tar
INSTALL ?= install
GIT ?= git

TARGET_BIN=containerd-fuse-overlayfs-grpc

VERSION ?= $(shell $(GIT) describe --match 'v[0-9]*' --dirty='.m' --always --tags)
VERSION_TRIMMED := $(VERSION:v%=%)
REVISION ?= $(shell $(GIT) rev-parse HEAD)$(shell if ! $(GIT) diff --no-ext-diff --quiet --exit-code; then $(ECHO) .m; fi)

PKG_MAIN := github.com/containerd/fuse-overlayfs-snapshotter/cmd/$(TARGET_BIN)
PKG_VERSION := github.com/containerd/fuse-overlayfs-snapshotter/cmd/$(TARGET_BIN)/version

export GO_BUILD=GO111MODULE=on CGO_ENABLED=0 $(GO) build -ldflags "-s -w -X $(PKG_VERSION).Version=$(VERSION) -X $(PKG_VERSION).Revision=$(REVISION)"

bin/$(TARGET_BIN):
	$(GO_BUILD) -o $@ $(PKG_MAIN)

all: binaries

help:
	@$(ECHO) "Usage: make <target>"
	@$(ECHO)
	@$(ECHO) " * 'install'   - Install binaries to system locations."
	@$(ECHO) " * 'uninstall' - Uninstall binaries from system."
	@$(ECHO) " * 'binaries'  - Build $(TARGET_BIN)."
	@$(ECHO) " * 'test'      - Run tests."
	@$(ECHO) " * 'clean'     - Clean artifacts."
	@$(ECHO) " * 'help'      - Show this help message."

binaries: bin/$(TARGET_BIN)

$(TARGET_BIN):
	$(GO_BUILD) -o $(CURDIR)/bin/$@ $(PKG_MAIN)

binaries: $(TARGET_BIN)

install:
	$(INSTALL) -D -m 755 $(CURDIR)/bin/$(TARGET_BIN) $(BINDIR)/$(TARGET_BIN)

uninstall:
	$(RM) $(BINDIR)/$(TARGET_BIN)

clean:
	$(RM) -r $(CURDIR)/bin $(CURDIR)/_output

TEST_DOCKER_IMG_TAG=containerd-fuse-overlayfs-test

test:
	DOCKER_BUILDKIT=1 $(DOCKER) build -t $(TEST_DOCKER_IMG_TAG) --build-arg FUSEOVERLAYFS_COMMIT=${FUSEOVERLAYFS_COMMIT} .
	$(DOCKER) run --rm $(TEST_DOCKER_IMG_TAG) fuse-overlayfs -V
	$(DOCKER) run --rm --security-opt seccomp=unconfined --security-opt apparmor=unconfined --device /dev/fuse $(TEST_DOCKER_IMG_TAG)
	$(DOCKER) rmi $(TEST_DOCKER_IMG_TAG)

_test:
	$(GO) test -exec rootlesskit -test.v -test.root

TAR_FLAGS=--transform 's/.*\///g' --owner=0 --group=0

ARTIFACT_NAME=containerd-fuse-overlayfs-$(VERSION_TRIMMED)

artifacts: clean
	$(MKDIR) -p _output
	GOOS=linux GOARCH=amd64 make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-amd64.tar.gz $(CURDIR)/bin/*
	GOOS=linux GOARCH=arm64 make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-arm64.tar.gz $(CURDIR)/bin/*
	GOOS=linux GOARCH=arm GOARM=7 make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-arm-v7.tar.gz $(CURDIR)/bin/*
	GOOS=linux GOARCH=ppc64le make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-ppc64le.tar.gz $(CURDIR)/bin/*
	GOOS=linux GOARCH=s390x make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-s390x.tar.gz $(CURDIR)/bin/*
	GOOS=linux GOARCH=riscv64 make -B
	$(TAR) $(TAR_FLAGS) -czvf _output/$(ARTIFACT_NAME)-linux-riscv64.tar.gz $(CURDIR)/bin/*

.PHONY: \
	$(TARGET_BIN) \
	install \
	uninstall \
	clean \
	test \
	_test \
	artifacts \
	help
