# Project Cloak demo v2

## Outline

In this demo we follow a bottom-up learning path: start from the low-level
concepts, then add abstractions.

Pros: leads to better understanding of "how it all works"
Cons: start with doing it "the hard way", the easy way is at the end

1. Write a basic workflow the hard way: typescript, raw queries, no extensions
2. Write a basic workflow the easy way: simplify by using an extension
3. Write an intermediate workflow: go, client stubs, several extensions
4. Move intermediate workflow to an extension
5. Write a new extension


## Scenario

I want to improve the automation on my project: todoapp.

Why?

* See all my workflows in one place
* Auto-caching makes everything faster
* Same behavior on everyone's machines
* Reuse workflows between dev and CI
* I can run expensive workflows on remote workers

### 1. Writing a basic workflow, the hard way

For my first workflow, I choose something simple that would benefit from running in Dagger: my yarn build.

Checkout the project repository:

```bash
git clone ssh://git@github.com/dagger/todoapp
```

Create a new directory for the build workflow:

```bash
mkdir -p todoapp/workflows/build
```

Write the workflow configuration file to `todoapp/workflows/build/cloak.yaml`:

```yaml
sdk: typescript
```

Write the workflow implementation to `todoapp/workflows/build/index.ts`:

FIXME: this is the easy way, expand to "the hard way" by removing yarn dependency

```typescript
// Build todoapp, the hard way
import { client, gql, getKey } from "@dagger.io/dagger";

// Install yarn in a container
// FIXME: do it the hard way, no alpine package
const base = await client.request(gql`
  {
    alpine {
      build(pkgs: ["yarn", "git"]) {
        id
      }
    }
  }
`)
.then((result: any) => result.alpine.build)

// Load app source code from working directory
const source = await client.request(gql`
  {
    host {
      workdir {
        id
      }
    }
  }
`)
.then((result: any) => result.host.workdir.id)

// Run 'yarn install'
const sourceWithDeps = await client.request(gql`
  {
    core {
      filesystem(id: "${base.id}") {
        exec(input: {
          args: ["yarn", "install"], 
          mounts: [{path: "/src", fs: "${source}"}],
          workdir: "/src",
        }) {
          mount(path: "/src") {
            id
          }
        }
      }
    }
  }
`)
.then((result: any) => result.core.filesystem.exec.mount);

// Run 'yarn run build'
const sourceWithBuild = await client.request(gql`
  {
    core {
      filesystem(id: "${base.id}") {
        exec(input: {
          args: ["yarn", "run", "build"],
          mounts: [{path: "/src", fs: "${sourceWithDeps.id}"}],
          workdir: "/src",
        }) {
          mount(path: "/src") {
            id
          }
        }
      }
    }
  }
`)
.then((result: any) => result.core.filesystem.exec.mount);
```

Write a typescript package file to `todoapp/workflows/build/package.json`:

```json
FIXME
```

And another file to `todoapp/workflows/build/tsconfig.json`:

```json
FIXME
```

Use the Dagger SDK to generate the rest of the code:

```bash
cloak -p todoapp/workflows/build generate
```

Run the workflow:

FIXME

It works! My workflow is running

Run it again:

FIXME

It's super fast because of caching.

### 2. Writing a basic workflow the easy way, using an extension

FIXME: clean up

  * Add yarn extension in my workflow dependencies `dagger.yaml`
    [P1 dependencies can be loaded from "fake universe", actually a configurable local directory]
    
  * Craft new, simpler queries in interactive sandbox
  * Simplify `index.ts`
  * Run `dagger do`: it works again!

### 3. Writing an intermediate workflow

  * Add `deploy` workflow in `dagger.yaml`
  * Write workflow implementation in `workflows/index.ts`
    * Craft new queries in sandbox (show that netlify is there)
    * [P1 worfklow can access project dir]
    * [P1 workflow can access environment variable]
  * Run `dagger do deploy`
  * Run again with extra parameters
    * [P2: support passing parameters to workflow]
    * [P1: consensus on how paramters will be passed to workflows in the future]

### 4. Moving a workflow to an extension

FIXME

### 5. Writing a new extension

FIXME

Vercel!
