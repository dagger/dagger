---
slug: /oy1q7/writing_extensions_nodejs
displayed_sidebar: "0.3"
---

# Writing a new project with NodeJS (Javascript or Typescript)

NOTE: It's currently hardcoded in this Dagger SDK to use yarn commands for building extensions, so it's safest to use yarn at the moment. There is [an open issue for improving this](https://github.com/dagger/dagger/issues/3036).

NOTE: For simplicity, these instructions also currently assume you are creating a new node module from scratch. Extensions can also be integrated into existing modules with some minor adjustments though.

## Setup the project configuration

1. Enter a node module directory for your project (i.e. a directory with a `package.json`)

   - If starting with an empty dir run:

     - `yarn init`
     - `yarn add @types/node typescript`
     - `yarn tsc --init --rootDir ext --outDir ./ext/dist --module ESNext --esModuleInterop --moduleResolution node --allowJs true`
     - Then manually add `"type": "module"` to your `package.json`
     - And manually update `tsconfig.json` w/ `"include": ["ext/**/*"]` on the top level (sibling to `"compilerOptions"`)

1. Add a dependency on the Dagger nodejs sdk:

   - `yarn add --dev git+https://github.com/dagger/dagger.git#cloak`
   - `yarn install`

1. Create a new project

   ```console
   dagger project init --name foo --sdk ts
   ```

   - Optionally add any dependencies you may want. For example, to add dependencies on the yarn and netlify examples you could run:

   ```console
   dagger project add git --remote https://github.com/dagger/dagger.git --ref cloak --path examples/yarn/dagger.json
   dagger project add git --remote https://github.com/dagger/dagger.git --ref cloak --path examples/netlify/ts/dagger.json
   ```

   - You'll now see a `dagger.json` file. You can view its contents directly (or run `dagger project`, which currently just dumps the contents)
   - You can also remove extensions with `dagger project rm --name <name>`
   - The dependencies are optional and just examples, feel free to change as needed.

## Create your extension

### Create schema files

- Create a new file `schema.graphql`, which will define the new APIs implemented by your extension and vended by your project.

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
    - [yarn](https://github.com/dagger/dagger/blob/cloak/examples/yarn/schema.graphql)
    - [netlify](https://github.com/dagger/dagger/blob/cloak/examples/netlify/ts/schema.graphql)
  - NOTE: this step may become optional in the future if code-first schemas are supported

### Implement the extension

1. Create and open `index.ts`, using the following as a skeleton for the implementation

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

1. Also feel free to import any other third-party dependencies as needed the same way you would with any other go project. They should all be installed and available when executing in the dagger engine.
1. Some more examples:
   - [yarn](https://github.com/dagger/dagger/blob/cloak/examples/yarn/index.ts)
   - [netlify](https://github.com/dagger/dagger/blob/cloak/examples/netlify/ts/index.ts)

### Invoke your extension

1. Add a `build` script to `package.json`, e.g.

   ```json
   "scripts": {
     "build": "tsc"
   }
   ```

1. One simple way to verify your extension builds and can be invoked is via the graphql playground.
   - Just run `dagger dev` from any directory in your project and navigate to `localhost:8080` in your browser (may need [an SSH tunnel](https://www.ssh.com/academy/ssh/tunneling-example) if on a remote host)
     - you can use the `--port` flag to override the port if needed
   - Click the "Docs" tab on the right to see the schemas available, including your extension and any dependencies.
   - You can submit queries by writing them on the lef-side pane and clicking the play button in the middle
1. You can also use the dagger CLI, e.g.

   ```console
   dagger do <<'EOF'
   {
     foo {
       bar(in: "in")
     }
   }
   EOF
   ```
