# syntax = docker/dockerfile:1.2

FROM golang:1.16.6-alpine AS build
WORKDIR /src
RUN apk add --no-cache file
ENV GOMODCACHE /root/.cache/gocache
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 go build -o /out/dagger -ldflags '-s -d -w' ./cmd/dagger; \
    file /out/dagger | grep "statically linked"

FROM scratch
COPY --from=build /out/dagger /bin/dagger
ENTRYPOINT ["/bin/dagger"]

