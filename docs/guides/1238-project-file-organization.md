---
slug: /1238/project-file-organization
displayed_sidebar: "0.2"
---

# Project file organization

As time will pass, your actions will get bigger and bigger. You will feel the need to better organize your file structure by moving some parts in subfolders.

The easiest way, when iterating fast, is to rely on the root module.

## CUE Module

A module in CUE is any directory including a `cue.mod` folder. It makes for the prefix/root of importable packages (e.g, `universe.dagger.io` is a module)

### Anonymous module (default)

The default module name is an empty string, which means it's anonymous. That's when you don't need to import other packages inside your module. It represents most cases where someone has a Dagger plan and imports everything from third-party packages, like universe.

### When to use a module

You can add a different module name if you want to import other packages from inside your module. This is required so you have a prefix/root before your imports. This is when you want to split your code into multiple files.

The module path becomes the import path prefix for packages in the module. This might be the name of a domain you own or another name you control (such as your company name), even your email, followed optionally by a descriptive path (e.g., project name).

You import packages by prefixing the module name they're a part of, plus the path to them, relative to the `cue.mod` directory.

```console
root                    // <- this is a module because it includes a cue.mod dir
|-- cue.mod
|   |-- module.cue      // module: "example.com/pkg"
|-- schemas
|   |-- compose         // <- this is a package because it includes files with a package directive
|   |   |-- spec.cue    // package compose
...
|-- plan.cue            // import "example.com/pkg/schemas/compose"
```

### Summary

Consider the root module as the URL to access the root of your project. Any subfolder inside this module needs to have Cue files with a package name equivalent to the dirname. The filename inside each folder is not important, the package name is:

```console
$  ls
bar       cue.mod   foo

$ cat foo/main.cue
cat foo/main.cue 
package foo

$ cat bar/anything.cue
package bar
```

To reference any of the code lying inside each of these packages:

```cue
import(
    "info@foo.bar/foo" // imports CUE code inside `/foo`
    "info@foo.bar/bar" // imports CUE code inside `/bar`
)
```

## Initializing the root module

### Project non initialized

Dagger holds a way to set a module name at project initialization: `dagger project init --name <NAME>`.

As said above, the name has to at least follow the structure of a domain: `domain.extension` or `domain.extension/project`. Email addresses are also accepted, and the referenced domain doesn't necessarly have to exist.

In our case, let's use a fake email address, for convenience purposes:

```console
dagger project init --name "info@foo.bar"
```

### Project already initialized

Manually edit the `cue.mod/module.cue` with the desired name:

```console
$ cat cue.mod/module.cue
module: "info@foo.bar"
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
dagger project init --name "info@foo.bar"
```

- Update `dagger` and `universe` dependencies

```console
dagger project update
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
        "universe.dagger.io/alpine"
        "universe.dagger.io/bash"

        "info@foo.bar/bar"
)

#Foo: {
    _img: alpine.#Build & {
            packages: {
                    bash: _
            }
    }

    bash.#Run & {
        env: TEST: bar.#Test
        always: true
        input:  _img.output
    }
}
```

Our `#Foo` definition is just a wrapper around `bash.#Run` that prefills the `input` field with a `bash` image (`_img` key above). Please notice that the `_img` step needs to reside inside the `#Foo` scope as it needs to exist inside the `action` to be executed.

For example, this doesn't work:

```cue title="foo/main.cue"
package foo

import (
        "universe.dagger.io/alpine"
        "universe.dagger.io/bash"

        "info@foo.bar/bar" // reference to the bar package
)

// _img is not being passed to the action as it lives outside #Foo
_img: alpine.#Build & {
        packages: {
                bash: _
        }
}

#Foo: bash.#Run & {
    env: TEST: bar.#Test // here, we reference the #Test definition present in the bar directory
    always: true
    input:  _img.output
}
```

- At the root of the project, create your `main.cue` file (file and package names are arbitrary):

```cue title="main.cue"
package main

import (
    "dagger.io/dagger"

    "info@foo.bar/foo" // reference to the foo package
)

dagger.#Plan & {
    actions: {
        hello: foo.#Foo & {
            script: contents: "echo \"Hello, inlined $TEST!\""
        }
    }
}
```

### Recap

We now have the directory structure shown below:

```console
$ tree  -L 2 .
.
├── bar
│   └── anything.cue
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   └── usr
├── foo
│   └── main.cue
└── main.cue

5 directories, 3 files
```

And the expected output is:

```console
$ dagger do hello --log-format plain
4:20PM INF actions.hello._img._dag."0"._op | computing
4:20PM INF actions.hello.script._write | computing
4:20PM INF actions.hello.script._write | completed    duration=0s
4:20PM INF actions.hello._img._dag."0"._op | completed    duration=1s
4:20PM INF actions.hello._img._dag."1"._exec | computing
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.141 fetch https://dl-cdn.alpinelinux.org/alpine/v3.15/main/x86_64/APKINDEX.tar.gz
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.366 fetch https://dl-cdn.alpinelinux.org/alpine/v3.15/community/x86_64/APKINDEX.tar.gz
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.611 (1/4) Installing ncurses-terminfo-base (6.3_p20211120-r0)
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.674 (2/4) Installing ncurses-libs (6.3_p20211120-r0)
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.706 (3/4) Installing readline (8.1.1-r0)
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.728 (4/4) Installing bash (5.1.16-r0)
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.805 Executing bash-5.1.16-r0.post-install
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.817 Executing busybox-1.34.1-r3.trigger
4:20PM INF actions.hello._img._dag."1"._exec | #4 0.900 OK: 8 MiB in 18 packages
4:20PM INF actions.hello._img._dag."1"._exec | completed    duration=1s
4:20PM INF actions.hello._exec | computing
4:20PM INF actions.hello._exec | completed    duration=200ms
4:20PM INF actions.hello._exec | #5 0.155 Hello, inlined world! # <== It was properly executed
```
