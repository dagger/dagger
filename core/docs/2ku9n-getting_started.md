---
slug: /2ku9n/getting_started
displayed_sidebar: "0.3"
---

# Getting started with Dagger

## Install

1. Setup BuildKitd
   - If you have `docker` installed locally and no `BUILDKIT_HOST` env var, `buildkitd` will be started automatically for you when you invoke `dagger`.
   - Otherwise, you can use the `BUILDKIT_HOST` env var to point to a running `buildkitd`. [More information here](https://docs.dagger.io/1223/custom-buildkit/).
2. Build `dagger` and make sure it's in your PATH
   - `go build ./cmd/dagger`
   - `ln -sf "$(pwd)/dagger" /usr/local/bin`
3. (Optional) Setup Docker Credentials
   - If you are receiving HTTP errors while pulling images from DockerHub, you might be getting rate-limited.
   - You can provide credentials to dagger by running `docker login` on your host and signing into a DockerHub account, which may help avoid these.

## Basic Invoking

Simple alpine example:

```console
dagger -p examples/alpine/dagger.json do <<'EOF'
{
  alpine{
    build(pkgs:["curl"]) {
      exec(input: {args:["curl", "https://dagger.io"]}) {
        stdout(lines: 1)
      }
    }
  }
}
EOF
```

Yarn build (output will just be encoded fs bytes for now, need to add export or shell util to dagger CLI interface):

```console
dagger -p examples/yarn/dagger.json do --local-dir source=. --set runArgs=build
```

## Development

## Cloak Dev Development

1. Running `./hack/make engine:build` will build a cloak executable inside `./bin/cloak` (If you don't want to use your own version reset `DAGGER_HOST` to an empty string)
2. Running `./bin/cloak dev` will start a dev server accepting queries and hosting a web interface at `http://localhost:8080`
3. By Setting the env variable `DAGGER_HOST=http://localhost:8080` the local dev instance of cloak will be used. Be careful as step 1, will use that version unless `DAGGER_HOST` is reset
4. Run tests or otherwise try out the modified version of cloak

### Invoking Actions

#### With Dagger CLI

TODO: document more, but see `Invoking` section above for some examples and `cmd/dagger/main.go` for implementation

#### With Embedded Go SDK

TODO: document more, but the idea here is that you can also write your own `main.go` that, similar to `cmd/dagger/main.go`, calls `engine.Start` and then do anything you want from there with the full power of Go rather than being limited to the CLI interface of `dagger`. Eventually, this embedding use case should be possible from any of our supported languages (e.g. Typescript).

### Modifying Core

TODO: document, currently just see `api/graphql.go` for existing core action implementations and schema definition.
