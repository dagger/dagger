# User Demo

This is a demo meant for external users. They are assumed to have general knowledge of what Dagger is, but aren't necessarily experts.

## 0. Setup

1. Ensure `dagger-buildkitd` is running (quickly invoke dagger if needed)
2. Build `cloak` and make sure it's in your PATH
   - `go build ./cmd/cloak`
   - `ln -sf "$(pwd)/cloak" /usr/local/bin`
3. Export `NETLIFY_AUTH_TOKEN` env
4. Ensure that all the steps are cached by running through them before demo. Sometimes buildkit automatic cache pruning logic will kick in after certain disk usage thresholds and you'll still have uncached steps during demo, but should be minimal.

## 1. Background

1. Previously, Dagger users implemented and orchestrated actions using CUE.
1. With Multi-lang, it's now possible to implement and orchestrate actions using a number of general-purpose programming languages such as Typescript and Go. This enables:
   - Using Dagger w/ languages you are more familiar with than CUE
   - Using libraries from other languages to implement actions (as opposed to creating shell wrappers)
   - Much more...
1. Additionally, actions written in one language are available to be called from any other language. If you want to write your action in Typescript, but someone else has written an action in Go that you'd like to use, you can import and call it seamlessly. We don't want fractured ecosystems.
1. In order to achieve interop between languages, we need a common API. For that we are using GraphQL

## 2. Low-level GraphQL API

Start with `cloak.yaml` in root of the repo where the non-core extensions are commented out, e.g.:

```yaml
name: "examples"
extensions:
  # - local: examples/alpine/cloak.yaml
  # - local: examples/yarn/cloak.yaml
  # - local: examples/netlify/go/cloak.yaml
  # - local: examples/todoapp/go/cloak.yaml
```

Run `cloak dev`, jump to GraphQL Playground (`http:localhost:8080`) and run following snippest

1.

```graphql
{
  core {
    image(ref: "alpine") {
      file(path: "/etc/alpine-release")
    }
  }
}
```

2.

```graphql
{
  core {
    image(ref: "alpine") {
      exec(input: { args: ["apk", "add", "curl"] }) {
        fs {
          exec(input: { args: ["curl", "--version"] }) {
            stdout
          }
        }
      }
    }
  }
}
```

3.

```graphql
{
  core {
    git(remote: "https://github.com/dagger/dagger") {
      file(path: "README.md")
    }
  }
}
```

4.

```graphql
{
  core {
    git(remote: "https://github.com/dagger/dagger") {
      dockerbuild(dockerfile: "Dockerfile") {
        exec(input: { args: ["dagger", "version"] }) {
          stdout
        }
      }
    }
  }
}
```

5.

```graphql
{
  core {
    git(remote: "https://github.com/dagger/dagger") {
      dockerbuild(dockerfile: "Dockerfile") {
        version: exec(input: { args: ["dagger", "version"] }) {
          stdout
        }
        help: exec(input: { args: ["dagger", "--help"] }) {
          stdout
        }
      }
    }
  }
}
```

6.

Now, go back to `cloak.yaml` in root of the repo and uncomment the extensions, e.g.

```yaml
name: "examples"
extensions:
  - local: examples/alpine/cloak.yaml
  - local: examples/yarn/cloak.yaml
  - local: examples/netlify/go/cloak.yaml
  - local: examples/todoapp/go/cloak.yaml
```

Then, restart `cloak dev` (ctrl-C to kill it) and jump back to the playground and you can run:

```graphql
{
  alpine {
    build(pkgs: ["jq", "curl"]) {
      jq: exec(input: { args: ["jq", "--version"] }) {
        stdout
      }
      curl: exec(input: { args: ["curl", "--version"] }) {
        stdout
      }
    }
  }
}
```

## 3. Action Implementations

### todoapp Deploy from CLI

1. Show todoapp docs from playground
1. Jump to CLI, show this command: `cloak -p examples/todoapp/go/cloak.yaml do Deploy --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN"`
   - explain args (`-p` points to the configuration of the todoapp extension, `do Deploy` tells cloak to run the Deploy op, `--local-dir` and `--secret` map to args in the deploy schema)
1. Run command, show the deployed URL

### Extension Implementations

1. From `examples/todoapp/go` show `schema.graphql`, `cloak.yaml` and `main.go`. Explain the schema, config and then map that to the implementation in `main.go`
   - For the TS flavored demo, go to `examples/todoapp/ts` and open `index.ts` instead of `main.go`.
1. Same as above, but show `examples/yarn` (implementation in `index.ts`)
   - Note use of raw graphql queries (TS has nice gql integration), how simple it is to express imperitive logic
1. Same as above, but show `examples/alpine` (implementation in `main.go`)
   - Note that `yarn` calls out to `alpine` but has no awareness it's invoking Go code from Typescript; cloak handles all the container magic in the background to make that happen
1. Same as above, but show `examples/netlify/go` (implementation in `main.go`) or `examples/netlify/ts` (implementation in `index.ts`)
   - Note that the 3rd party netlify client is just imported and used; can import whatever libraries you want. This code is then invokable from any other language and also wrapped up in dagger caching goodness.
