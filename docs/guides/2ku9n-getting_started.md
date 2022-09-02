---
slug: /2ku9n/getting_started
displayed_sidebar: '0.3'
---

# Getting started with Cloak

## Install

1. Setup BuildKitd
   - If you have `docker` installed locally and no `BUILDKIT_HOST` env var, `buildkitd` will be started automatically for you when you invoke `cloak`.
   - Otherwise, you can use the `BUILDKIT_HOST` env var to point to a running `buildkitd`. [More information here](https://docs.dagger.io/1223/custom-buildkit/).
2. Build `cloak` and make sure it's in your PATH
   - `go build ./cmd/cloak`
   - `ln -sf "$(pwd)/cloak" /usr/local/bin`
   - Alternative: create a bash alias like `alias cloak="go run /absolute/path/to/the/cloak/repo/cmd/cloak"`
     - This results in cloak rebuilding every time in case you are making lots of changes to it

## Basic Invoking

Simple alpine example:

```console
cloak -p examples/alpine/cloak.yaml do <<'EOF'
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

Yarn build (output will just be encoded fs bytes for now, need to add export or shell util to cloak CLI interface):

```console
cloak -p examples/yarn/cloak.yaml do --local-dir source=. --set runArgs=build
```

## Development

### Invoking Actions

#### With Cloak CLI

TODO: document more, but see `Invoking` section above for some examples and `cmd/cloak/main.go` for implementation

#### With Embedded Go SDK

TODO: document more, but the idea here is that you can also write your own `main.go` that, similar to `cmd/cloak/main.go`, calls `engine.Start` and then do anything you want from there with the full power of Go rather than being limited to the CLI interface of `cloak`. Eventually, this embedding use case should be possible from any of our supported languages (e.g. Typescript).

### Modifying Core

TODO: document, currently just see `api/graphql.go` for existing core action implementations and schema definition.
