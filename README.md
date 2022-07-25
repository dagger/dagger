# Cloak

## Setup

```console
# The following shouldn't be needed anymore as they are now built on-the-fly when the package is imported. Preserved here if you want to test package builds independently.
docker run -d -p 5555:5000 --name registry registry:2

docker build -f ./Dockerfile.alpine -t localhost:5555/dagger:alpine . && docker push localhost:5555/dagger:alpine

yarn --cwd ./sdk/nodejs/dagger build && yarn --cwd examples/graphql_ts upgrade dagger && yarn --cwd examples/graphql_ts build && docker build -f ./Dockerfile.graphql_ts -t localhost:5555/dagger:graphql_ts . && docker push localhost:5555/dagger:graphql_ts
```

## Invoking

Simple alpine example (no output yet, need another flag in cloak cli):

```console
go run cmd/cloak/main.go -f examples/alpine/dagger.yaml <<'EOF'
{alpine{build(pkgs:["jq"])}}
EOF
```

TODOApp deploy example:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -local-dirs src=examples/todoapp/app -secrets token="$NETLIFY_AUTH_TOKEN" <<'EOF'
query Build($local_src: FS!, $secret_token: String!) {
    todoapp{deploy(src: $local_src, token: $secret_token){url}}
}
EOF
```
