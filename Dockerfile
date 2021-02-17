# syntax = docker/dockerfile-upstream:experimental@sha256:398a0a10f19875add7fe359a37f2f971c46746b064faf876776ae632a3472c37

FROM golang:1.16-alpine AS build
WORKDIR /src
RUN apk add --no-cache file
RUN --mount=target=. --mount=target=/root/.cache,type=cache \
    CGO_ENABLED=0 go build -o /out/dagger ./cmd/dagger && file /out/dagger | grep "statically linked"

FROM scratch
COPY --from=build /out/dagger /bin/dagger
ENTRYPOINT ["/bin/dagger"]

