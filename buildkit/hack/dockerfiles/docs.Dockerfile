# syntax=docker/dockerfile:1

ARG GO_VERSION=1.21
ARG ALPINE_VERSION=3.19

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS golatest

FROM golatest AS buildctl
WORKDIR /src
ENV CGO_ENABLED=0
ARG TARGETPLATFORM
RUN --mount=target=. \
  --mount=target=/root/.cache,type=cache \
  --mount=target=/go/pkg/mod,type=cache \
  go build -mod=vendor -o /usr/bin/buildctl ./cmd/buildctl

FROM golatest AS docsgen
WORKDIR /src
ENV CGO_ENABLED=0
RUN --mount=target=. \
  --mount=target=/root/.cache,type=cache \
  --mount=target=/go/pkg/mod,type=cache \
  go build -mod=vendor -o /out/docsgen ./docs/generate.go

FROM alpine AS gen
RUN apk add --no-cache rsync git
WORKDIR /src
COPY --from=docsgen /out/docsgen /usr/bin
COPY --from=buildctl /usr/bin/buildctl /usr/bin/
RUN --mount=target=/context \
  --mount=target=.,type=tmpfs <<EOT
set -e
rsync -a /context/. .
docsgen
mkdir /out
cp -r docs/* /out
EOT

FROM scratch AS update
COPY --from=gen /out /

FROM gen AS validate
RUN --mount=target=/context \
  --mount=target=.,type=tmpfs <<EOT
set -e
rsync -a /context/. .
git add -A
rm -rf docs/*
cp -rf /out/* ./docs/
if [ -n "$(git status --porcelain -- docs/)" ]; then
  echo >&2 'ERROR: Docs result differs. Please update with "make docs"'
  git status --porcelain -- docs/
  exit 1
fi
EOT
