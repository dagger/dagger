---
slug: /1239/making-reusable-package
displayed_sidebar: "0.2"
---

# Making reusable packages

Whilst splitting your project into several files is a good idea, you will sometimes need to create standalone packages aiming to be reusable and shared. Let's explore how to do that.

## Packages, modules, and Dagger projects

Understanding the difference between a package, a module and a Dagger project is an important distinction, as it will help better organizing your work:

- **CUE Package**: directory with CUE files, each including a package definition on top, making it importable (e.g, `universe.dagger.io/docker` is a package, `universe.dagger.io/docker/cli` is another package);
- **CUE Module**: directory with a `cue.mod` directory which makes for the prefix/root of importable packages (e.g, `universe.dagger.io` is a module);
- **Dagger Project**: a complete Dagger configuration, loaded from a valid CUE module or package.

## End-to-End example

Instead of splitting all of our files in the the same project with a module, we could directly make reusable components from our project as standalone packages. For the sake of the exercise, we will use Github as a version control system, but it will work with any alternative.

### Create the base Dagger project

First, let's start with a basic project:

- Create the root directory

```console
mkdir rootProject && cd rootProject
```

- Initialize the project

```console
dagger project init
```

- Update `dagger` and `universe` dependencies

```console
dagger project update
```

- At the root of the project, create your `main.cue` file (file and package names are arbitrary):

```cue title="rootProject/main.cue"
package main

import (
    "dagger.io/dagger"
)

dagger.#Project & {
    actions: {
        hello: #Run & {
            script: contents: "echo \"Hello!\""
        }
    }
}
```

We are currently calling a `#Run` definition that doesn't exist yet. We will declare it in a new package right below: it will wrap the `bash.#Run` definition with a custom image.

### Create the package

Let's see how to create the `#Run` definition in its own package:

- Create a completely new directory next to `rootProject`: it will be a new git repository.
You can directly `git clone` or initialize a new one with:

```console
cd .. && mkdir personal && cd personal && git init
```

- At the root of this new folder, create the CUE file that will contain the `#Run` package. The name of the file is not important, but the package name shall follow the name of the remote repo (best practice convention):

```cue title=personal/main.cue
package personal

import(
  "universe.dagger.io/alpine"
  "universe.dagger.io/bash"
)

#Run: {
    _img: alpine.#Build & {
        packages: bash: _
    }

    bash.#Run & {
        always: true
        input:  _img.output
    }
}
```

You should now have this file and directory structure:

```console
$ tree -L 2
.
├── personal
│   └── main.cue
└── rootProject
    ├── cue.mod
    └── main.cue

3 directories, 2 files
```

### Link package to the project

- We first need to include the newly created package in the project. In order to do that, we will create a symlink similar to what `dagger project update` would create once we push the package on Github:

```console
$ ls -l
total 0
drwxr-xr-x  4 home  wheel  128  9 mai 16:04 personal
drwxr-xr-x  4 home  wheel  128  9 mai 16:01 rootProject

$ mkdir -p rootProject/cue.mod/pkg/github.com/your-username/

$ ln -s "$(pwd)/personal" "$(pwd)/rootProject/cue.mod/pkg/github.com/your-username/personal"
```

When using your package from `dagger project update` in the `rootProject` directory, the actual packager manager would copy the files from the repository in the `rootProject/cue.mod/pkg/github.com/your-username/personal` folder.

- We then need to change the project's `main.cue` to call the `#Run` definition in the `personal` package that we just built:

```cue title="rootProject/main.cue"
package main

import (
    "dagger.io/dagger"

    "github.com/your-username/personal" // import personal package
)

dagger.#Plan & {
    actions: {
        hello: personal.#Run & { // reference #Run definition from personal package imported above
            script: contents: "echo \"Hello!\""
        }
    }
}
```

### Run the project

Now that we have connected all the dots, let's run our action to see if it works:

```console
$ cd rootProject

$ dagger do hello --log-format plain
4:42PM INF actions.hello.script._write | computing
4:42PM INF actions.hello._img._dag."0"._op | computing
4:42PM INF actions.hello.script._write | completed    duration=0s
4:42PM INF actions.hello._img._dag."0"._op | completed    duration=0s
4:42PM INF actions.hello._img._dag."1"._exec | computing
4:42PM INF actions.hello._img._dag."1"._exec | completed    duration=0s
4:42PM INF actions.hello._exec | computing
4:42PM INF actions.hello._exec | completed    duration=200ms
4:42PM INF actions.hello._exec | #5 0.123 Hello!
```

### Push package on repository

Now that we made sure we correctly built our package, we only need to push it to the repository:

- Add
- Commit
- Tag
- Push

On another project, you will directly be able to retrieve the `personal` package using the `dagger project update url@tag` command.

<u>Reminder:</u>

*The name of the repository should follow the name of the created folder and the package name (`personal` in the above example)*
