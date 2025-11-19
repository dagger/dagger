# syntax=docker/dockerfile:1

ARG GO_VERSION=1.21
ARG GOLANGCI_LINT_VERSION=1.54.2

FROM golang:${GO_VERSION}-alpine
RUN apk add --no-cache git gcc musl-dev
ENV GOFLAGS="-buildvcs=false"
ARG GOLANGCI_LINT_VERSION
RUN wget -O- -nv https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s v${GOLANGCI_LINT_VERSION}
WORKDIR /go/src/github.com/tonistiigi/fsutil
RUN --mount=target=. \
    --mount=target=/root/.cache,type=cache \
    --mount=target=/go/pkg/mod,type=cache \
    golangci-lint run
