# Project Cloak

Project Cloak is an experimental project to add multi-language support to Dagger.

## Alpha Software Warning

Cloak is alpha-quality software and is still under active development. It is not a finished product!
You will certainly encounter bugs, confusing behavior, and incomplete documentation. Please tell us everything!

## Early Access

Project Cloak is currently in early access for a small group of testers. Early Access includes the following:

- Early access to the [Project Cloak repository](https://github.com/dagger/cloak)
- Early access to the Project Cloak community channel on Discord: #cloak
- Our eternal gratitude for trying unfinished software and contributing precious feedback.
- Sweet Dagger swag :)

We appreciate any participation in the project, including:

- Asking and answering questions on the Discord channel
- Sharing feedback of any kind
- Going through documentation and tutorials, and telling us how it went
- Opening github issues to report bugs and request features
- Contributing code and documentation
- Suggesting people to invite to the Project Cloak Early Access program

## Getting Started

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
cd ./examples/alpine
cloak query <<'EOF'
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
cloak query -c examples/yarn/cloak.yaml --local-dir source=examples/todoapp/app --set name=build
```

TODOApp deploy:

```console
cd ./examples/todoapp/ts
cloak query --op Deploy --local-dir src=../app --secret token="$NETLIFY_AUTH_TOKEN"
```

## Development

### Invoking Actions

#### With Cloak CLI

TODO: document more, but see `Invoking` section above for some examples and `cmd/cloak/main.go` for implementation

#### With Embedded Go SDK

TODO: document more, but the idea here is that you can also write your own `main.go` that, similar to `cmd/cloak/main.go`, calls `engine.Start` and then do anything you want from there with the full power of Go rather than being limited to the CLI interface of `cloak`. Eventually, this embedding use case should be possible from any of our supported languages (e.g. Typescript).

- A (slightly outdated) example of this can be found in `cmd/demo/main.go`

### Creating a new extension in Typescript

TODO: automate and simplify the below

TODO: add instructions for client stub generation (these instructions work w/ raw graphql queries right now)

Say we are creating a new extension, written in Typescript, called `foo` that will have a single action `bar`.

1. Setup the extension configuration
   1. Copy the existing `yarn` extension to a new directory for the new extension:
      - `cp -r examples/yarn examples/foo`
   1. `cd examples/foo`
   1. `rm -rf app node_modules yarn.lock`
   1. Open `Dockerfile` and change occurences of `examples/yarn` to `examples/foo`
   1. Open `package.json`, replace occurences of `dagger-yarn` with `foo`
   1. Open `schema.graphql`, replace the existing `build`, `test` and `deploy` fields under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your extension is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `cloak.yaml`
      - This is where you declare your extension, and other extensions that it depends on. All extensions declared in this file will be built, loaded, and available to be called from your own extension.
      - Currently, cloak builds extensions by looking for a Dockerfile in the extension source directory. In the future we will offer more flexibility in how extensions can be built.
      - Replace the existing `yarn` key under `extensions` with `foo`; similarly change `examples/yarn/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the extensions you want to be able to call from your extensions. They all follow the same format right now
      - You don't need to declare `core` as a dependency: it is built-in and always available to all extensions.
1. Implement your action by editing `index.ts`
   - Replace each of the existing `Script` field under `const resolver` with an implementation for your action (or add multiple fields if implementing multiple actions).
   - The `args` parameter is an object with a field for each of the input args to your action (as defined in `schema.graphql`
   - You should use `FSID` when accepting a filesystem as an input

### Creating a new extension in Go

Say we are creating a new Dagger extension, written in Go, called `foo` that will have a single action `bar`.

1. Setup the extension configuration
   1. Starting from the root of the cloak repo, make a new directory for your action:
      - `mkdir -p examples/foo`
   1. `cd examples/foo`
   1. Setup the Dockerfile that will build your action
      - `cp ../alpine/Dockerfile .`
      - Open `Dockerfile` and change occurences of `examples/alpine` to `examples/foo`
      - TODO: this is boilerplate that will go away soon
   1. Open `schema.graphql`, replace the existing `build` field under `Query` with one field per action you want to implement
      - This is where the schema for the actions in your extension is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `cloak.yaml`
      - This is where you declare your extension, and other extensions that it depends on. All extensions declared in this file will be built, loaded, and available to be called from your own extension.
      - Currently, cloak builds extensions by looking for a Dockerfile in the extension source directory. In the future we will offer more flexibility in how extensions can be built.
      - Replace the existing `alpine` key under `extensions` with `foo`; similarly change `examples/alpine/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the extensions you want to be able to call from your actions. They all follow the same format right now
      - You don't need to declare `core` as a dependency: it is built-in and always available to all extensions.
      
1. Generate client stubs and implementation stubs
   - From `examples/foo`, run `cloak generate --output-dir=. --sdk=go`
   - Now you should see client stubs for each of your dependencies under `gen/<pkgname>` in addition to helpers for your implementation under `gen/foo`
   - Additionally, there should now be a `main.go` file with a stub implementations.
1. Implement your action by replacing the panics in `main.go` with the actual implementation.
   - When you need to call a dependency, import it from paths like `github.com/dagger/cloak/examples/foo/gen/<dependency pkgname>`

### Modifying Core

TODO: document, currently just see `api/graphql.go` for existing core action implementations and schema definition.
