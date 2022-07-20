# Cloak

## Setup

```console
docker run -d -p 5555:5000 --name registry registry:2

docker build -f ./Dockerfile.alpine -t localhost:5555/dagger:alpine . && docker push localhost:5555/dagger:alpine

yarn --cwd ./sdk/nodejs/dagger build && yarn --cwd examples/helloworld_ts upgrade dagger && yarn --cwd examples/helloworld_ts build && docker build -f ./Dockerfile.helloworld_ts -t localhost:5555/dagger:helloworld_ts . && docker push localhost:5555/dagger:helloworld_ts

yarn --cwd ./sdk/nodejs/dagger build && yarn --cwd examples/graphql_ts upgrade dagger && yarn --cwd examples/graphql_ts build && docker build -f ./Dockerfile.graphql_ts -t localhost:5555/dagger:graphql_ts . && docker push localhost:5555/dagger:graphql_ts
```

## Invoking

```console
go run ./cmd/test/main.go
```
