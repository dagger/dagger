# Core API proposal

This directory contains a proposal for a complete GraphQL API for Dagger/Cloak. It is written with the following goals in mind:

1. Feature parity with Dagger 0.2
2. Close to feature parity with Buildkit, with an incremental path to reaching full parity in the future
3. Follow established graphql best practices
4. Sove as many outstanding DX problems as possible

## Reference

* [Environment](environment.gql)
* [Container](container.gql)
* [Directory](directory.gql)
* [File](file.gql)
* [Git](git.gql)
* [HTTP](http.gql)
* [Secret](secret.gql)

## DX problems solved

Some problems in the cloak DX that are not yet resolved, and this proposal would help solve, include:

* Uncertainty as to how to express uploads in a graphql-friendly way (deployment, image push, git push, etc)
* Chaining of FS operations greatly reduces verbosity, but cannot be applied all the time
* Loading extensions from custom locations requires re-inventing parts of our graphql API in a mini-DSL in cloak.yaml
* Transitioning a script to an extension requires non-trivial refactoring, to tease apart the script-specific code from the underlying "API".
* The API sandbox is amazing for prototyping small queries, but of limited use when multiple queries must reference each other. This is because the native code doing the stitching cannot be run by the playground, so filesystem IDs must be manually copy-pasted between queries.

## Design highlights

### `withX` and `withoutX`

To avoid the use of rpc-style verbs (a graphql best practice) and maximize chaining (a strength of our DX), we use the terminology `withX` and `withoutX`.

A field of the form *withX* returns the same object with content *X* added or changed.

Example:

```graphql
"An empty directory with a README copied to it"
query readmeDir($readme: FileID!) {
  directory {
    withCopiedFile(source: $readme, path: "README.md") {
      id
    }
}

"An empty container with an app directory mounted into it"
query appContainer($app: DirectoryID!) {
  container {
    withMountedDirectory(source: $app, path: "/app") {
      id
    }
  }
}
```

A field of the form *withoutX* returns the same object with content *X* removed.

```graphql
"Remove node_modules from a JS project"
query removeNodeModules($dir: DirectoryID!) {
  directory(id: $dir) {
    withoutDirectory(path: "node_modules") {
      id
    }
  }
}
```

### Secrets

Secret handling has been simplified and made more consistent with Directory handling.

* Secrets have an ID, and can be loaded by ID in the standard graphql manner
* Secrets can be created in one of two ways:
    1. From an environment variable: `type Environment { secret }`
    2. From a file: `type Directory { secret }`

### Embrace the llb / Dockerfile model

The `Container` type proposes an expansive definition of the container, similar to the Buildkit/Dockerfile model. A `Container` is:

1. A filesystem state
2. An OCI artifact which can be pulled from, and pushed to a repository at any time
3. A persistent configuration to be applied to all commands executed inside it

This is similar to how buildkit models llb state, and how the Dockerfile language models stage state. Note that Cloak extends this model to include even mount configuration (which are scoped to exec in buildkit, but scoped to container in cloak).

Examples:

```graphql
"""
Download a file over HTTP in a very convoluted way:

1. Download a base linux container
2. Install curl
3. Download the file into the container
4. Load and return the file
"""
query convolutedDownload($url: String!) {
  container {
    from(address: "index.docker.io/alpine:latest") {
      exec(args: ["apk", "add", "curl"]) {
        exec(args: ["curl", "-o", "/tmp/download", $url) {
          file(path: "/tmp/download") {
            id
         }
      }
    }
  }
}

"""
Specialize two containers from a common base
"""
query twoContainers {
  container {
    from(address: "alpine") {
      debug: withVariable(name: "DEBUG", value: "1") {
        id
        exec(args: ["env"]) {
          stdout
        }
      }
      noDebug: withVariable(name: "DEBUG", value: "0") {
        id
        exec(args: ["env"]) {
          stdout
        }
      }
    }
  }
}
```