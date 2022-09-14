# syntax = docker/dockerfile:1

FROM golang:1.19-alpine AS build-base
WORKDIR /src
RUN apk add --no-cache file git
ENV GOMODCACHE /root/.cache/gocache

# Build dagger-buildkitd linux binary
FROM build-base AS build-linux
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 GOOS=linux go build -o /bin/dagger -ldflags '-s -d -w' ./cmd/dagger

# Build dagger binary
FROM build-base AS build
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 go build -o /bin/dagger -ldflags '-s -d -w' ./cmd/dagger

# serve dagger-buildkitd from alpine
FROM alpine:3.16 AS dagger-buildkitd
RUN apk add -U --no-cache runc git
COPY --from=docker:20.10.17-cli-alpine3.16 /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build-linux /bin/dagger /bin/dagger
# RUN ln -s $(which dagger) /usr/bin/buildctl
ENTRYPOINT ["/bin/dagger", "buildkitd"]

# serve dagger from alpine
FROM alpine:3.16
RUN apk add -U --no-cache ca-certificates
COPY --from=docker:20.10.17-cli-alpine3.16 /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /bin/dagger /bin/dagger
ENTRYPOINT ["/bin/dagger"]
