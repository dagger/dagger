# Cloak

## Setup

1. Ensure `dagger-buildkitd` is running (invoke dagger if needed)
   - TODO: should port code from dagger for setting this up automatically to here in cloak
2. Build `cloak` and make sure it's in your PATH
   - `go build ./cmd/cloak`
   - `ln -sf "$(pwd)/cloak" /usr/local/bin`

## Basic Invoking

Simple alpine example (output will just be the encoded FS bytes for now, need to add export+shell util to `cloak` CLI):

```console
cd ./examples/alpine
cloak query <<'EOF'
{alpine{build(pkgs:["jq","curl"])}}
EOF
```

Yarn build:

```console
cloak query -c examples/yarn/dagger.yaml --local-dir source=examples/todoapp/app --set name=build
```

TODOApp deploy:

```console
cloak query -c examples/todoapp/ts/dagger.yaml --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN" <<'EOF'
query Build($src: FS!, $token: String!) {
    todoapp{deploy(src: $src, token: $token){url}}
}
EOF
```

## Development

### Invoking Actions

#### With Cloak CLI

TODO: document more, but see `Invoking` section above for some examples and `cmd/cloak/main.go` for implementation

#### With Embedded Go SDK

TODO: document more, but the idea here is that you can also write your own `main.go` that, similar to `cmd/cloak/main.go`, calls `engine.Start` and then do anything you want from there with the full power of Go rather than being limited to the CLI interface of `cloak`. Eventually, this embedding use case should be possible from any of our supported languages (e.g. Typescript).

- A (slightly outdated) example of this can be found in `cmd/demo/main.go`

### Creating a new Typescript action

TODO: automate and simplify the below

TODO: these instructions currently skip client stub generation for dependencies because the raw graphql interface is okay enough. See `examples/graphql_ts` for example use of generated client stubs.

Say we are creating a new Typescript package called `foo` that will have a single action `bar`.

1. Setup the package configuration
   1. Copy the existing `yarn` package to a new directory for the new package:
      - `cp -r examples/yarn examples/foo`
   1. `cd examples/foo`
   1. `rm -rf app node_modules yarn.lock`
   1. Open `Dockerfile` and change occurences of `examples/yarn` to `examples/foo`
   1. Open `package.json`, replace occurences of `dagger-yarn` with `foo`
   1. Open `schema.graphql`, replace the existing `build`, `test` and `deploy` fields under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your package is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `dagger.yaml`
      - This is where you declare your own package in addition to dependencies of your package. Declaring packages here makes them available to be called by your action implementation in addition to telling cloak how to build them.
      - Packages are declared by specifying how they are built. Currently, we just use Dockerfiles for everything, but in theory this should be much more flexible.
      - Replacing the existing `yarn` key under `actions` with `foo`; similarly change `examples/yarn/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the packages you want to be able to call from your actions. They all follow the same format right now
      - The only package you don't need to declare a dependency on is `core`, it's inherently always a dep
1. Implement your action by editing `index.ts`
   - Replace each of the existing `build`, `test` and `deploy` fields under `resolver.Query` with one implementation for each action.
   - The `args` parameter is an object with a field for each of the input args to your action (as defined in `schema.graphql`
   - The `FS` type will be of type `string` (as that's the representation of the `FS` scalar type in our graphql schema at the moment)

### Creating a new Go package

TODO: automate and simplify the below

Say we are creating a new Go package called `foo` that will have a single action `bar`.

1. Setup the package configuration
   1. Copy the existing `alpine` package to a new directory for the new package:
      - `cp -r examples/alpine examples/foo`
   1. `cd examples/foo`
   1. `rm -rf alpine.go gen`
   1. Open `Dockerfile` and change occurences of `examples/alpine` to `examples/foo`
   1. Open `gqlgen.yml` and replace every occurence of `alpine` with `foo`
      - This configures the code generation tool we use to create implementation stubs
   1. Open `schema.graphql`, replace the existing `build` field under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your package is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `dagger.yaml`
      - This is where you declare your own package in addition to dependencies of your package. Declaring packages here makes them available to be called by your action implementation in addition to telling cloak how to build them.
      - Packages are declared by specifying how they are built. Currently, we just use Dockerfiles for everything, but in theory this should be much more flexible.
      - Replacing the existing `alpine` key under `actions` with `foo`; similarly change `examples/alpine/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the packages you want to be able to call from your actions. They all follow the same format right now
      - The only package you don't need to declare a dependency on is `core`, it's inherently always a dep
   1. Setup client stub configuration for each of your dependencies
      - `cloak generate --output-dir gen`
        - This will parse your `dagger.yaml` and export `schema.graphql` and `operation.graphql` into a subdir under `gen/` for each of your dependencies (plus `core`)
      - For each of the dependencies
        - Create a file `gen/<pkgname>/genqclient.yaml` based on `../alpinegen/core/genqlient.yaml`, replacing the word `core` with `<pkgname>`
        - Add a `//go:generate` directive to the top of `main.go` in the form:
          - `//go:generate go run github.com/Khan/genqlient ./gen/<pkgname>/genqlient.yaml`
1. Generate client stubs and implementation stubs
   - From `examples/foo`, run `go generate main.go`
   - Now you should see client stubs for each of your dependencies under `gen/<pkgname>` in addition to helpers for your implementation under `gen/<foo>`
   - Additionally, there should now be a `foo.go` file with a stub implementation.
1. Implement your action by replacing the panic in `foo.go` with the actual implementation.
   - When you need to call a dependency, import it from under `gen/<pkgname>`

### Modifying Core

TODO: document, currently just see `api/graphql.go` for existing core action implementations and schema definition.
