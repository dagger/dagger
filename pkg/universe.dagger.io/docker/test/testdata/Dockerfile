###
# STAGE: builder
# Build a simple go program
# GO TO STAGE: app
###
FROM golang:1.17-alpine as builder

WORKDIR /app

COPY go.mod .
COPY main.go .

RUN go build -o hello main.go

FROM alpine:3.15@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300

COPY --from=builder /app/hello /bin/hello

CMD ["hello"]
