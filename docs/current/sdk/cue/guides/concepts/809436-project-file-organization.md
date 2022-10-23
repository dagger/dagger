---
slug: /sdk/cue/809436/project-file-organization
displayed_sidebar: 'current'
---

# Project file organization

When your configuration grows, you may feel the need to better organize your project by splitting it into multiple files.

A simple way to accomplish this is to create and import packages within your project's module.

## What is a CUE module?

A [module in CUE](https://cuelang.org/docs/concepts/packages/#modules) is any directory including a `cue.mod` folder. It makes for the prefix/root of importable packages. For example, `universe.dagger.io` is a module which includes several packages.

When you ran `dagger-cue project init`, *dagger* created this directory for you.

:::tip

Learn more about CUE modules in [Modules, Packages, and Instances — How files are organized in CUE](https://cuelang.org/docs/concepts/packages/).

:::

### Anonymous module (default)

When the module name is an empty string, it's known as an anonymous module:

```cue title="cue.mod/module.cue"
module: ""
```

Anonymous modules are used when we don't need to import other packages from within the same module.

By default, this is how projects start: a single CUE file (e.g. `dagger.cue`), which imports everything from third-party packages, such as `universe.dagger.io/docker`.

### When to name a module

If you want to create packages inside your project, and import from each other, you need to give a name to your module. This is required so you have a prefix/root before your imports.

The module path becomes the import path prefix for packages in the module. This might be the name of a domain you own or another name you control (such as your company name), even your email, followed optionally by a descriptive path (e.g., project name). The address doesn't have to exist, it's only used for namespacing.

You import packages by **prefixing the module name they're a part of, plus the path** to them, relative to the `cue.mod` directory.

```console
root                  // <- this is a module because it includes a cue.mod dir
|-- cue.mod
|   |-- module.cue    // module: "example.com/myproject"
|-- schemas
|   |-- compose       // <- this is a package because it includes files with a package directive
|   |   |-- spec.cue  // package compose
...
|-- dagger.cue        // import "example.com/myproject/schemas/compose"
```

### Summary

Consider the module as the URL to access the root of your project. Any subfolder inside this module needs to have CUE files with a package name equivalent to the directory name. File names inside each directory are not important, the package name is.

## Initializing the module

### Project non initialized

The module name can be set during project initialization: `dagger-cue project init --name <NAME>`.

Let's use an email address for convenience purposes:

```console
dagger-cue project init --name "info@example.com"
```

### Project already initialized

Manually edit the desired name in `module.cue`:

```cue title="cue.mod/module.cue"
module: "info@example.com"
```

## Creating packages in subdirectories

### Practice

Let's put everything above into practice:

- Initialize the workdir environment

```console
mkdir daggerTest && cd daggerTest
```

- Initialize the project

```console
dagger-cue project init --name "info@example.com"
```

- Install `dagger.io` and `universe.dagger.io` dependencies

```console
dagger-cue project update
```

- Create 2 subfolders: `foo` and `bar`:

```console
mkdir foo bar
```

- Inside the `bar` folder, create a CUE file with any name and whose package is `bar` (same as parent directory):

```cue title="bar/anything.cue"
package bar

#Test: "world"
```

- Inside the `foo` folder, create a CUE file with any name and whose package is `foo` (same as parent directory):

```cue title="foo/main.cue"
package foo

import (
        "universe.dagger.io/bash"

        "info@example.com/bar"
)

#Foo: {
    script: string

    _run: bash.#RunSimple & {
        // reference the #Test definition from the bar directory
        env: TEST: bar.#Test

        // store the output of the command
        "script": contents: "\(script) > /output.txt"
        export: files: "/output.txt": _

        // don't cache
        always: true
    }

    result: _run.export.files."/output.txt"
}
```

- At the root of the project, create your `main.cue` file (file and package names are arbitrary):

```cue title="main.cue"
package main

import (
    "dagger.io/dagger"

    "info@example.com/foo"
)

dagger.#Plan & {
    actions: {
        hello: foo.#Foo & {
            script: "echo -n Hello, inlined $TEST!"
        }
    }
}
```

### Recap

We now have the directory structure shown below:

```console
$ tree -L 2 .
.
├── bar
│   └── anything.cue
├── cue.mod
│   ├── module.cue
│   ├── pkg
├── foo
│   └── main.cue
└── main.cue

4 directories, 4 files
```

And the expected output is:

```console
$ dagger-cue do hello
[✔] actions.hello
Field   Value
result  "Hello, inlined world!"
```
