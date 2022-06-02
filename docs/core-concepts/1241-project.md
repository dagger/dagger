---
slug: /1241/project
displayed_sidebar: '0.2'
---

# Anatomy of a Dagger Project

## What is a project?

A Dagger project is a complete configuration for running dagger. It defines:

* A set of [actions](https://docs.dagger.io/1221/action) which can be invoked with 'dagger do'
* Interactions with the client machine: read or write files, access environment variables, execute commands, etc.

## Creating a project

A project can be created by running `dagger project init`. This creates a `cue.mod` directory and, if you use the `--template`
flag, will also create a sample configuration.

## Directory layout

A project is loaded from a *project directory*. By default, `dagger` will use the current directory, but that can be changed with the `-p` flag.

The only constraint for the project directory is that it can be loaded by the Cue interpreter. This requires the following:

* One or more valid CUE files. Filenames does not matter, in doubt use `dagger.cue`.
* If there are multiple CUE files, they will be merged together. This requires using the same `package` name in all files (or no package at all).
* A `cue.mod` directory must exist either in the project directory, or in a parent directory. This is required for vendoring and loading CUE dependencies.

Here is an example of a project directory layout:

```shell
.
├── cue.mod
│   ├── dagger.mod
│   ├── dagger.sum
│   ├── module.cue
│   ├── pkg
│   └── usr
├── dagger.cue
├── README.md
├── src
│   ├── App.js
│   ├── components
│   │   ├── FilterButton.js
│   │   ├── Form.js
│   │   └── Todo.js
│   ├── index.css
│   └── index.js
```

In this example, `cue.mod` and `dagger.cue` are specific to the Dagger project; the other files are not.
This is a common pattern: most Dagger projects are created by adding files to an existing directory.
The location of the project is important, as [actions can access the directory where they are defined](https://docs.dagger.io/1240/core-source).

## Configuration schema

Once loaded from the project directory, the project configuration must implement
the schema defined in [`dagger.io/dagger.#Project`](https://github.com/dagger/dagger/blob/main/pkg/dagger.io/dagger/project.cue#L4-L48).

Here is an example of project configuration:

```cue
package hello

import (
        "dagger.io/dagger"
        "universe.dagger.io/bash"
        "universe.dagger.io/alpine"

)

dagger.#Project & {
        actions: {
                _alpine: alpine.#Build & {
                        packages: bash: _
                }

                // Hello world
                hello: bash.#Run & {
                        input: _alpine.output
                        script: contents: "echo Hello World"
                        always: true
                }
        }
}
```

In the context of this project, the action "hello" can be invoked with `dagger do hello`.
