# Writing a new extension in Go

Say we are creating a new Dagger extension, written in Go, called `foo` that will have a single action `bar`.

1. Setup the extension configuration
   1. Starting from the root of the cloak repo, make a new directory for your action:
      - `mkdir -p examples/foo`
   1. `cd examples/foo`
   1. `cp ../alpine/Dockerfile .`
   1. Open `schema.graphql`, replace the existing `build` field under `Alpine` with one field per action you want to implement. Replace all occurences of `Alpine` with `Foo`.
      - This is where the schema for the actions in your extension is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `cloak.yaml`
      - This is where you declare your extension, and other extensions that it depends on. All extensions declared in this file will be built, loaded, and available to be called from your own extension.
      - Currently, cloak builds extensions by looking for a Dockerfile in the extension source directory. In the future we will offer more flexibility in how extensions can be built.
      - Replace the existing `alpine` key under `extensions` with `foo`; similarly change `examples/alpine/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the extensions you want to be able to call from your actions. They all follow the same format right now
      - You don't need to declare `core` as a dependency: it is built-in and always available to all extensions.
1. Generate client stubs and implementation stubs
   - From `examples/foo`, run `cloak --context=../.. -p examples/foo/cloak.yaml generate --output-dir=. --sdk=go`
   - Now you should see client stubs for each of your dependencies under `gen/<pkgname>` in addition to structures for needed types in `models.go` and some auto-generated boilerplate that makes your code invokable in `generated.go`
   - Additionally, there should now be a `main.go` file with a stub implementations.
1. Implement your action by replacing the panics in `main.go` with the actual implementation.
   - When you need to call a dependency, import it from paths like `github.com/dagger/cloak/examples/foo/gen/<dependency pkgname>`
