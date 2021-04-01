# syntax=docker/dockerfile:1.2
ARG GO_VERSION=1.16

FROM --platform=$BUILDPLATFORM crazymax/goreleaser-xx:0.161.1 AS goreleaser-xx
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS base
RUN apk add --no-cache gcc git musl-dev
COPY --from=goreleaser-xx / /
WORKDIR /src

FROM base AS vendored
RUN --mount=type=bind,target=.,rw \
  --mount=type=cache,target=/go/pkg/mod \
  go mod tidy && go mod download && \
  mkdir /out && cp go.mod go.sum /out

FROM vendored AS vendor-validate
RUN --mount=type=bind,target=.,rw \
  git add -A && cp -rf /out/* .; \
  if [ -n "$(git status --porcelain -- go.mod go.sum)" ]; then \
    echo >&2 'ERROR: Vendor result differs. Please vendor your package with "docker buildx bake vendor-update"'; \
    git status --porcelain -- go.mod go.sum; \
    exit 1; \
  fi

FROM scratch AS vendor-update
COPY --from=vendored /out /

FROM vendored AS test
RUN --mount=type=bind,target=. \
  --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  go test -v -coverprofile=/tmp/coverage.txt -covermode=atomic -race ./...

FROM scratch AS test-coverage
COPY --from=test /tmp/coverage.txt /coverage.txt

FROM vendored AS build-debug
RUN --mount=type=bind,target=/src,rw \
  --mount=type=cache,target=/root/.cache/go-build \
  --mount=target=/go/pkg/mod,type=cache \
  go build -race -o /tmp/dagger-debug ./cmd/dagger/

FROM crazymax/docker:20.10.5 AS docker
FROM alpine AS e2e
RUN apk add --no-cache bash gcc gnupg ncurses shellcheck
COPY --from=build-debug /tmp/dagger-debug /usr/bin/dagger-debug
COPY --from=mozilla/sops:v3-alpine /usr/local/bin/sops /usr/local/bin/sops
COPY --from=docker /usr/libexec/docker/cli-plugins/docker-buildx /usr/libexec/docker/cli-plugins/docker-buildx
COPY --from=docker /usr/local/bin/buildkitd /usr/bin/buildkitd
COPY --from=docker /usr/local/bin/buildctl /usr/bin/buildctl
COPY --from=docker /usr/local/bin/docker /usr/bin/docker
WORKDIR /src
ENV DAGGER_BINARY="/usr/bin/dagger-debug"
COPY . .

FROM vendored AS golangci-lint
RUN --mount=type=bind,target=. \
  --mount=type=cache,target=/root/.cache/go-build \
  --mount=type=cache,target=/root/.cache/golangci-lint \
  --mount=from=golangci/golangci-lint:v1.37-alpine,source=/usr/bin/golangci-lint,target=/usr/bin/golangci-lint \
  --mount=from=cuelang/cue,source=/usr/bin/cue,target=/usr/bin/cue \
  golangci-lint run --timeout 10m0s ./...

FROM vendored AS cue-fmt
RUN --mount=type=bind,target=. \
  --mount=from=cuelang/cue,source=/usr/bin/cue,target=/usr/bin/cue \
  cue fmt -s ./stdlib/... \
  && cue fmt -s ./examples/*/ \
  && cue fmt -s ./tests/...

FROM vendored AS build
ARG TARGETPLATFORM
ARG GIT_REF
RUN --mount=type=bind,target=/src,rw \
  --mount=type=cache,target=/root/.cache/go-build \
  --mount=target=/go/pkg/mod,type=cache \
  goreleaser-xx --debug \
    --name "dagger" \
    --dist "/dist" \
    --main "./cmd/dagger/" \
    --ldflags="-s -w -X 'main.Version={{.Version}}'" \
    --files="LICENSE" \
    --files="README.md"

FROM scratch AS artifact
COPY --from=build /dist/*.tar.gz /
COPY --from=build /dist/*.zip /

FROM alpine
RUN apk --update --no-cache add shadow \
  && addgroup -g 1000 dagger \
  && adduser -u 1000 -G dagger -s /sbin/nologin -D dagger
COPY --from=build /usr/local/bin/dagger /usr/local/bin/dagger
USER dagger
ENTRYPOINT [ "dagger" ]
