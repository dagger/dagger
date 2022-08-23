# Writing a new extension in Go

Say we are creating a new Dagger extension, written in Go, called `foo` that will have a single action `bar`.

1. Setup the extension configuration
   1. Starting from the root of the cloak repo, make a new directory for your action:
      - `mkdir -p examples/foo`
   1. `cd examples/foo`
   1. `cp ../alpine/schema.graphql ../alpine/operations.graphql .`
   1. Open `schema.graphql`, replace the existing `build` field under `Alpine` with one field per action you want to implement. Replace all occurences of `Alpine` with `Foo`.
      - This is where the schema for the actions in your extension is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open `operations.graphql`, make similar updates as those to `schema.graphql`
      - This file defines operations available to clients (such as the cloak CLI or generated code clients).
      - In most cases, this file is easy to derive from `schema.graphql`; we thus expect to be able to autogenerate it for many cases and make its creation optional in the long term.
   1. Create a new file called `cloak.yaml`
      - This is where you declare your extension, and other extensions that it depends on. All extensions declared in this file will be built, loaded, and available to be called from your own extension.
      - Create the file in the following format:
      ```yaml
      name: foo
      sources:
        - path: .
          sdk: go
      dependencies:
        - local: ../../<dependencyA>/cloak.yaml
        - local: ../../<dependencyB>/cloak.yaml
      ```
      - `<dependencyX>` should be replaced with the directory of the extension you want a dependency on. `core` does not need to be declared as a dependency; it is implicitly included. If your only dependency is `core`, then you can just skip the `dependencies:` key entirely.
1. Generate client stubs and implementation stubs
   - From `examples/foo`, run `cloak --context=../.. -p examples/foo/cloak.yaml generate --output-dir=. --sdk=go`
   - Now you should see client stubs for each of your dependencies under `gen/<pkgname>` in addition to structures for needed types in `models.go` and some auto-generated boilerplate that makes your code invokable in `generated.go`
   - Additionally, there should now be a `main.go` file with a stub implementations.
1. Implement your action by replacing the panics in `main.go` with the actual implementation.
   - When you need to call a dependency, import it from paths like `github.com/dagger/cloak/examples/foo/gen/<dependency pkgname>`
