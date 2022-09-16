FROM golang:1.18.2-alpine AS build
WORKDIR /src
RUN apk add --no-cache file git
RUN go mod init go.dagger.io/dagger/shim/cmd
COPY . .
RUN CGO_ENABLED=0 go build -o /_shim -ldflags '-s -d -w' .

FROM scratch
COPY --from=build /_shim /_shim
