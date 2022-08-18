# Getting started with Cloak

## Install

1. Ensure `dagger-buildkitd` is running (invoke dagger if needed)
   - TODO: should port code from dagger for setting this up automatically to here in cloak
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
cloak -p examples/yarn/cloak.yaml do --local-dir source=examples/todoapp/app --set name=build
```

TODOApp deploy:

```console
cloak -p examples/todoapp/ts/cloak.yaml do Deploy --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN"
```

## Development

### Invoking Actions

#### With Cloak CLI

TODO: document more, but see `Invoking` section above for some examples and `cmd/cloak/main.go` for implementation

#### With Embedded Go SDK

TODO: document more, but the idea here is that you can also write your own `main.go` that, similar to `cmd/cloak/main.go`, calls `engine.Start` and then do anything you want from there with the full power of Go rather than being limited to the CLI interface of `cloak`. Eventually, this embedding use case should be possible from any of our supported languages (e.g. Typescript).

- A (slightly outdated) example of this can be found in `cmd/demo/main.go`


### Modifying Core

TODO: document, currently just see `api/graphql.go` for existing core action implementations and schema definition.
