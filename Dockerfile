# syntax = docker/dockerfile:1

FROM golang:1.19-alpine AS build
WORKDIR /src
RUN apk add --no-cache file git
ENV GOMODCACHE /root/.cache/gocache
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 go build -o /bin/dagger -ldflags '-s -d -w' ./cmd/dagger

FROM alpine:3.17.0
RUN apk add -U --no-cache ca-certificates
COPY --from=docker:20.10.17-cli-alpine3.16 /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /bin/dagger /bin/dagger
ENTRYPOINT ["/bin/dagger"]
