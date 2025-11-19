# syntax=docker/dockerfile:1

ARG GO_VERSION=1.21

FROM golang:${GO_VERSION}-alpine AS gomod
RUN  apk add --no-cache git
WORKDIR /src
RUN --mount=target=/src,rw \
  --mount=target=/go/pkg/mod,type=cache \
  go mod tidy && \
  mkdir /out && cp -r go.mod go.sum /out && \
  cd bench && go mod tidy && \
  mkdir /out/bench && cp -r go.mod go.sum /out/bench

FROM scratch AS update
COPY --from=gomod /out /

FROM gomod AS validate
RUN --mount=target=.,rw \
  git add -A && \
  cp -rf /out/* . && \
  ./hack/validate-gomod check
