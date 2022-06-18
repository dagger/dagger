# Cloak

## Setup

```console
docker run -d -p 5555:5000 --name registry registry:2
docker build -f ./Dockerfile.core -t localhost:5555/dagger:core . && docker push localhost:5555/dagger:core
docker build -f ./Dockerfile.alpine -t localhost:5555/dagger:alpine . && docker push localhost:5555/dagger:alpine
```

## Invoking

```console
go run ./cmd/cloak localhost:5555/dagger:alpine build '{"packages": ["bash", "jq"]}'
```

## Stubbing

```console
go generate ./examples/alpine
```
