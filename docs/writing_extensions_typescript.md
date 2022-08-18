# Writing an API extension in Typescript

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
   1. Open `schema.graphql`, replace the existing `build`, `test` and `deploy` fields under `Yarn` with one field per action you want to implement. Replace all occurences of `Yarn` with `Foo`.
      - This is where the schema for the actions in your extension is configured. Feel free to add more complex output/input types as needed
      - If you want `foo` to just have a single action `bar`, you just need a field for `bar` (with appropriate input and output types).
   1. Open up `cloak.yaml`
      - This is where you declare your extension, and other extensions that it depends on. All extensions declared in this file will be built, loaded, and available to be called from your own extension.
      - Currently, cloak builds extensions by looking for a Dockerfile in the extension source directory. In the future we will offer more flexibility in how extensions can be built.
      - Replace the existing `name: "yarn"` with `name: "foo"`; similarly change `examples/yarn/Dockerfile` to `examples/foo/Dockerfile`
      - Add similar entries for each of the extensions you want to be able to call from your extensions. They all follow the same format right now
      - You don't need to declare `core` as a dependency: it is built-in and always available to all extensions.
1. Implement your action by editing `index.ts`
   - Replace the existing `Yarn` field under `const resolver` with `Foo`. Also replace the existing `script` field with an implementation for your action (or add multiple fields if implementing multiple actions).
   - The `args` parameter is an object with a field for each of the input args to your action (as defined in `schema.graphql`
   - You should use `FSID` when accepting a filesystem as an input, `SecretID` (also imported from `@dagger.io/dagger`) when accepting a secret as input.
