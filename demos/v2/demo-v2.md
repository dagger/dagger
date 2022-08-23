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

## Open questions

Some parts of the demo are under discussion. Resolving these discussions may cause the demo scenario to change.

### Bottom-up or top-down?

* *bottom-up*: start by showing fundamentals, then gradually add features which simplify the experience.
* *top-down*: start with the simplest example possible, using many features, then gradually explain how each feature works under the hood.

At the moment this demo is *bottom-up*: first we write a workflow "the hard way"; then we add an extension; then client-generated stubs; etc. 

It can be adapted to a top-down flow, by swapping steps 1 and 2.

### Project-centric or workflow-centric?

*Project-centric*:
  * One config file at the root of a project (eg. todoapp)
  * All the project's workflows, extensions and dependencies are defined in one file
  * Cloak API allows access to project directory, ie `host { projectDirectory { read, write }}`.
  * Project dir is automatically set and not configurable
  * A project may import other projects as dependencies. Dependencies' extensions are loaded
  * Dependencies may be declared globally for the project, or independently for each workflow and extension. 

*Workflow-centric*
  * One directory per workflow, each with its own config file (eg "build", "deploy")
  * One directory per extension, each with its own config file (eg "netlify", "yarn")
  * No first class project concept
  * Cloak API allows access to workdir, ie `host { workdir { read, write }}`.
  * User is responsible for setting correct workdir. Defaults to client workdir, can be overridden with `CLOAK_WORKDIR` environment variable
  * A workflow may import extensions. Extensions may import extensions.

At the moment this demo assumes a *workflow-centric* design.

Pros:
* Simpler config files
* One directory = one self-contained component which dagger can build and run
* Less concepts (no first-class project)
* Simpler dependency system (1 dependency = 1 directory + 1 config file)
* Generally less magic

Cons:
* More config files
* No familiar "drop one file in your project and go" UX, similar to docker compose, docker build, waypoint etc.
* Project directory is not magically available, requires more user intervention
* Can't load multiple related extensions in one dependency: must either merge into one extension, or (if using different SDKs) import each individually

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

With the help of the API sandbox, let's write the workflow configuration file to `todoapp/workflows/build/cloak.yaml`:

```yaml
sdk: typescript
```

Let's open the API sandbox to figure out what to write in our workflow:

```bash
cloak dev
```

With the help of the API sandbox, we can write the workflow implementation to `todoapp/workflows/build/index.ts`:

FIXME: this is the easy way, expand to "the hard way" by removing yarn dependency

```typescript
// Build todoapp, the hard way
import { gql, Engine } from "@dagger.io/dagger";

new Engine().run(async (client: GraphQLClient) => {
  // 1. Load app source code from working directory
  const source = await client.request(gql`
    {
      host {
        workdir {
          read {
            id
          }
        }
      }
    }
  `)
  .then((result: any) => result.host.workdir)

  // 2. Install yarn in a container
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

  // 3. Run 'yarn install' in a container
  const sourceWithDeps = await client.request(gql`
    {
      core {
        filesystem(id: "${base.id}") {
          exec(input: {
            args: ["yarn", "install"], 
            mounts: [{path: "/src", fs: "${source.id}"}],
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

  // 4. Run 'yarn run build' in a container
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

  // 5. write the result back to workdir
  client.request(gql`
  {
    host {
      workdir {
        write(contents: ${sourceWithBuild.id})
      }
    }
  }
  `)
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

Run the workflow (FIXME: how??)

```bash
FIXME.
```

It works! My workflow is running

Run it again:

```bash
FIXME
```

My workflow is now running in containers.
It's super fast because of caching, portable, and easy to scale.

### 2. Writing a basic workflow the easy way, using an extension

Let's make our workflow simpler by using a crucial feature of Dagger: *API extensions*. Most of our code just tells cloak to 1) build a container with yarn installed, and 2) execute that container in a certain way. What if we the Dagger API already knew how to do that? That's what API extensions are for.

Add a dependency to the yarn extension in your workflow configuration file, and write it to `todoapp/workflows/build/cloak.yaml`:

```yaml
sdk: typescript
dependencies:
  -
    source: git
    remote: ssh://git@github.com/dagger/cloak
    ref: main
    path: examples/yarn/ts
```

*Note: since the cloak repository is still private, make sure your machine is correctly configured with ssh access*

*FIXME: cloak does not yet support transient dependencies. Since `yarn` currently depends on `alpine`, that dependency needs to be added also.*


Relaunch the API sandbox to explore new available API queries:

```bash
cloak dev
```

Edit the workflow implementation to use the yarn extension in our API calls. Note that the implementation is now much shorter:

```typescript
// Build todoapp, the hard way
import { gql, Engine } from "@dagger.io/dagger";

new Engine().run(async (client: GraphQLClient) => {

  // 1. Load app source code from working directory
  const source = await client.request(gql`
    {
      host {
        workdir {
          id
        }
      }
    }
  `)
  .then((result: any) => result.host.workdir)

  // 2. Run yarn build script
  const build = await client.request(gql`
  {
    yarn {
      project(source: "${source.id}") {
        script (name: "build") {
          run {
            output
          }
        }
      }
    }
  }
  `)
  .then({result: any} => result.yarn.project.script.run.output)

  // 3. Write build result to workdir
  await client.request(gql`
  {
    host {
      workdir {
        write(contents: "${build.id}")
      }
    }
  }
  `)
})
```

Run the workflow:

```bash
FIXME.
```

It works! My simplified workflow is running.

Run it again:

```bash
FIXME
```

### 3. Writing an intermediate workflow

Let's write a second workflow.

* We'll use our understanding of extensions to build something more ambitious: deploying a live staging environment of our app.
* We'll use Yarn to build, and Netlify to deploy.
* This time we will use Go.
* We will show another convenient feature of Dagger SDKs: auto-generated client libraries. This is a nice-to-have in typescript, but mandatory in Go for a pleasant developer experience.

Create a new workflow directory:

```bash
mkdir -p todoapp/workflows/deploy
```

Write a new configuration file, with the required dependencies, to `todoapp/workflows/deploy/cloak.yaml`:

```yaml
sdk: go
workflows:
  deploy:
    source: ./..
    dependencies:
    -
        source: git
        remote: ssh://git@github.com/dagger/cloak
        ref: main
        path: examples/yarn/ts
    -
        source: git
        remote: ssh://git@github.com/dagger/cloak
        ref: main
        path: examples/netlify/ts
```

Open the API sandbox:

```bash
cloak dev
```

Write a new workflow implementation to `todoapp/workflows/deploy/main.go`:

```go
package main

import (
  "os"
)

// FIXME

token, exists := os.Environ["NETLIFY_TOKEN"]
```

Build and run your workflow:

```bash
# FIXME: how to pass the correct workdir?
(
  cd todoapp
  go run workflows/deploy
)
```

```bash
CLOAK_WORKDIR=$(pwd)/todoapp go run ./todoapp/workflows/deploy
```


It worked!



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

```FIXME```

### 5. Writing a new extension

FIXME

Vercel!
