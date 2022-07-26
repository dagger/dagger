# Cloak

## Setup

1. Ensure `dagger-buildkitd` is running (invoke dagger if needed)
   - TODO: should port code from dagger for setting this up automatically to here in cloak

## Basic Invoking

Simple alpine example (output will just be the encoded FS bytes for now, need to add export+shell util to `cloak` CLI):

```console
go run cmd/cloak/main.go -f examples/alpine/dagger.yaml <<'EOF'
{alpine{build(pkgs:["jq","curl"])}}
EOF
```

Yarn build:

```console
go run cmd/cloak/main.go -f examples/yarn/dagger.yaml -q examples/yarn/operations.graphql -op Script -local-dirs source=examples/todoapp/app -set name=build
```

TODOApp deploy:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -local-dirs src=examples/todoapp/app -secrets token="$NETLIFY_AUTH_TOKEN" <<'EOF'
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

1. Setup the Dockerfile used to build the package
   1. From the root of the repo, run `cp Dockerfile.todoapp Dockerfile.foo`
   1. Open `Dockerfile.foo` and change occurences of `examples/todoapp` to `examples/foo`
1. Setup the package configuration
   1. Copy the existing `todoapp` package to a new directory for the new package:
      - `cp -r examples/todoapp examples/foo`
   1. `cd examples/foo`
   1. `rm -rf app node_modules yarn.lock`
   1. Open `package.json`, replace occurences of `todoapp` with `foo`
   1. Open `dagger.graphql`, replace the existing `build`, `test` and `deploy` fields under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your package is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `dagger.yaml`
      - This is where you declare your own package in addition to dependencies of your package. Declaring packages here makes them available to be called by your action implementation in addition to telling cloak how to build them.
      - Packages are declared by specifying how they are built. Currently, we just use Dockerfiles for everything, but in theory this should be much more flexible.
      - Replacing the existing `alpine` key under `actions` with `foo`; similarly change `Dockerfile.alpine` to `Dockerfile.foo`
      - Add similar entries for each of the packages you want to be able to call from your actions. They all follow the same format right now
      - The only package you don't need to declare a dependency on is `core`, it's inherently always a dep
1. Implement your action by editing `index.ts`
   - Replace each of the existing `build`, `test` and `deploy` fields under `resolver.Query` with one implementation for each action.
   - The `args` parameter is an object with a field for each of the input args to your action (as defined in `dagger.graphql`
   - The `FS` type will be of type `string` (as that's the representation of the `FS` scalar type in our graphql schema at the moment)

### Creating a new Go package

TODO: automate and simplify the below

Say we are creating a new Go package called `foo` that will have a single action `bar`.

1. Setup the Dockerfile used to build the package
   1. From the root of the repo, run `cp Dockerfile.alpine Dockerfile.foo`
   1. Open `Dockerfile.foo` and change occurences of `examples/alpine` to `examples/foo`
1. Setup the package configuration
   1. Copy the existing `alpine` package to a new directory for the new package:
      - `cp -r examples/alpine examples/foo`
   1. `cd examples/foo`
   1. `rm -rf alpine.go gen/alpine`
   1. Open `gqlgen.yml` and replace every occurence of `alpine` with `foo`
      - This configures the code generation tool we use to create implementation stubs
   1. Open `dagger.graphql`, replace the existing `build` field under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your package is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `dagger.yaml`
      - This is where you declare your own package in addition to dependencies of your package. Declaring packages here makes them available to be called by your action implementation in addition to telling cloak how to build them.
      - Packages are declared by specifying how they are built. Currently, we just use Dockerfiles for everything, but in theory this should be much more flexible.
      - Replacing the existing `alpine` key under `actions` with `foo`; similarly change `Dockerfile.alpine` to `Dockerfile.foo`
      - Add similar entries for each of the packages you want to be able to call from your actions. They all follow the same format right now
      - The only package you don't need to declare a dependency on is `core`, it's inherently always a dep
   1. Setup client stub configuration for each of your dependencies
      - This is by far the ugliest part right now, desperately needs more automation
      - For each of the dependencies you declared in `dagger.yaml` that wasn't your own package (i.e. `foo`):
        - Create a directory `gen/<pkgname>`
        - Declare the schema in `gen/<pkgname>/<pkgname>.graphql`.
          - Note that in this case, you need use a slightly different format as now all the actions are not directly under `Query`. See for example `gen/core/core.graphql` where `Query` has `core: Core!` and `type Core` is where the actual actions are defined.
        - Create a file `gen/<pkgname>/operations.graphql`, put an operation for each action from the package you want to call. See `gen/core/operations.graphql` as a reference.
        - Create a file `gen/<pkgname>/genqclient.yaml` based on `gen/core/genqlient.yaml`, replacing the word `core` with `<pkgname>`
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
