# syntax = docker/dockerfile:1

FROM golang:1.18.2-alpine AS build
WORKDIR /src
RUN apk add --no-cache file git
ENV GOMODCACHE /root/.cache/gocache
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 go build -o /out -ldflags '-s -d -w' ./examples/netlify/go

FROM alpine:3.15
COPY --link --from=build /out /entrypoint
COPY --link ./examples/netlify/go/schema.graphql /schema.graphql
COPY --link ./examples/netlify/go/operations.graphql /operations.graphql
ENTRYPOINT ["/entrypoint"]
