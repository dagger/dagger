---
slug: /oy1q7/writing_extensions_nodejs
displayed_sidebar: "0.3"
---

# Writing a new project with NodeJS (Javascript or Typescript)

Say we are creating a new project called `foo`. It will have

1. A single extension, written in Typescript, the extends the schema with an action called `bar`.
1. A script, written in Javascript, that can call the extension (and any other project dependencies)

NOTE: It's currently hardcoded in this cloak SDK to use yarn commands for building extensions, so it's safest to use yarn at the moment. There is [an open issue for improving this](https://github.com/dagger/cloak/issues/146).

NOTE: For simplicity, these instructions also currently assume you are creating a new node module from scratch. Extensions and scripts can also be integrated into existing modules with some minor adjustments though.

## Setup the project configuration

1. Enter a node module directory for your project (i.e. a directory with a `package.json`)

   - If starting with an empty dir run:

     - `yarn init`
     - `yarn add @types/node typescript`
     - `yarn tsc --init --rootDir ext --outDir ./ext/dist --module ESNext --esModuleInterop --moduleResolution node --allowJs true`
     - Then manually add `"type": "module"` to your `package.json`
     - And manually update `tsconfig.json` w/ `"include": ["ext/**/*"]` on the top level (sibling to `"compilerOptions"`)

1. In order to pull cloak dependencies, cloak and yarn will need the ability to pull a private git repo

   - Setting up an ssh-agent with credentials that can pull the `dagger/cloak` will cover all cases and is recommended for now.
     - Github has [documentation on setting this up for various platforms](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/generating-a-new-ssh-key-and-adding-it-to-the-ssh-agent#adding-your-ssh-key-to-the-ssh-agent).
     - Be sure that the `SSH_AUTH_SOCK` variable is set in your current terminal (running `eval "$(ssh-agent -s)"` will typically take care of that)
     - Without this, you may get error messages containing `no ssh handler for id default`

1. Add a dependency on the cloak nodejs sdk:

   - `yarn add --dev git+ssh://git@github.com:dagger/cloak.git#main`
   - `yarn install`

1. Create a new file called `cloak.yaml`

   - This is where you declare your project, and other project that it depends on. All extensions declared in this file will be built, loaded, and available to be called when the project is loaded into cloak.
   - Create the file in the following format:

   ```yaml
   name: foo
   scripts:
     - path: script
       sdk: ts
   dependencies:
     - git:
         remote: git@github.com:dagger/cloak.git
         ref: main
         path: examples/yarn/cloak.yaml
     - git:
         remote: git@github.com:dagger/cloak.git
         ref: main
         path: examples/netlify/go/cloak.yaml
   ```

   - The dependencies are optional and just examples, feel free to change as needed.
   - `core` does not need to be explicitly declared as a dependency; it is implicitly included. If your only dependency is `core`, then you can just skip the `dependencies:` key entirely.

## Create your script

Create and open `script/index.mjs`. An example implementation that just pulls an image and reads a file is:

```javascript
import { gql, Engine } from "@dagger.io/dagger";
new Engine().run(async (client) => {
  const fileContents = await client
    .request(
      gql`
        {
          core {
            image(ref: "alpine") {
              file(path: "/etc/alpine-release")
            }
          }
        }
      `
    )
    .then((result) => result.core.image.file);
  console.log("Output: " + fileContents);
});
```

The simplest way to then invoke your script is to execute `node script/index.mjs`.

This can also be added to the scripts in `package.json` for more convenient invocation.

## Create your extension

Update your `cloak.yaml` to include a new `extensions` key:

```yaml
name: foo
scripts:
  - path: script
    sdk: ts
extensions:
  - path: ext
    sdk: ts
dependencies:
  - git:
      remote: git@github.com:dagger/cloak.git
      ref: main
      path: examples/yarn/cloak.yaml
  - git:
      remote: git@github.com:dagger/cloak.git
      ref: main
      path: examples/netlify/go/cloak.yaml
```

### Create schema files

- Create a new file `ext/schema.graphql`, which will define the new APIs implemented by your extension and vended by your project.

  - Example contents for a single `bar` action:

    ```graphql
    extend type Query {
      foo: Foo!
    }

    type Foo {
      bar(in: String!): String!
    }
    ```

  - Also see other examples:
    - [yarn](https://github.com/dagger/cloak/blob/main/examples/yarn/schema.graphql)
    - [netlify](https://github.com/dagger/cloak/blob/main/examples/netlify/ts/schema.graphql)
  - NOTE: this step may become optional in the future if code-first schemas are supported

### Implement the extension

1. Create and open `ext/index.ts`, using the following as a skeleton for the implementation

```typescript
import { client, DaggerServer, gql } from "@dagger.io/dagger";

const resolvers = {
  Foo: {
    bar: async (args: { in: string }) => {
      // implementation goes here
    },
  },
};

const server = new DaggerServer({
  resolvers,
});

server.run();
```

1. Also feel free to import any other third-party dependencies as needed the same way you would with any other go project. They should all be installed and available when executing in the cloak engine.
1. Some more examples:
   - [yarn](https://github.com/dagger/cloak/blob/main/examples/yarn/index.ts)
   - [netlify](https://github.com/dagger/cloak/blob/main/examples/netlify/ts/index.ts)

### Invoke your extension

1. Add a `build` script to `package.json`, e.g.

   ```json
   "scripts": {
     "build": "tsc"
   }
   ```

1. One simple way to verify your extension builds and can be invoked is via the graphql playground.
   - Just run `cloak dev` from any directory in your project and navigate to `localhost:8080` in your browser (may need [an SSH tunnel](https://www.ssh.com/academy/ssh/tunneling-example) if on a remote host)
     - you can use the `--port` flag to override the port if needed
   - Click the "Docs" tab on the right to see the schemas available, including your extension and any dependencies.
   - You can submit queries by writing them on the lef-side pane and clicking the play button in the middle
1. You can also use the cloak CLI, e.g.

   ```console
   cloak do <<'EOF'
   {
     foo {
       bar(in: "in")
     }
   }
   EOF
   ```

1. Finally, you should now be able to invoke your extension from your script too, e.g.

```javascript
import { gql, Engine } from "@dagger.io/dagger";
new Engine().run(async (client) => {
  const output = await client
    .request(
      gql`
        {
          foo {
            bar(in: "in")
          }
        }
      `
    )
    .then((result) => result.foo.bar);
  console.log("Output: " + output);
});
```
