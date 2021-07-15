# Dagger 109: creating packages

This tutorial illustrates how to create new packages, manually distribute them among your applications and contribute to
the Dagger stdlib packages.

## Creating your own package

### Initializing workspace

Create an empty directory for your new Dagger workspace:

```shell
mkdir workspace
cd workspace
```

As described in the previous tutorials, initialize your Dagger workspace:

```shell
dagger init
```

That will create 2 directories: `.dagger` and `cue.mod` where our package will reside:

```shell
.
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   └── usr
├── .dagger
│   └── env
```

### Writing the package

Now that you've initialized your workspace it's time to write a simple package. Package name usually starts with a
domain name (as in Go) followed with a descriptive name. In this example we will use my Github domain name and call our
package `echo` because it will print a single line to stdout.

```shell
mkdir -p cue.mod/pkg/github.com/tjovicic/echo
```

Let's write the package logic. It will spin up an alpine container and print out `Hello` to stdout:

```shell
touch cue.mod/pkg/github.com/tjovicic/echo/source.cue
```

```cue title="cue.mod/pkg/github.com/tjovicic/echo/source.cue"
package echo

import (
  "alpha.dagger.io/alpine"
  "alpha.dagger.io/os"
)

ctr: os.#Container & {
  image: alpine.#Image & {
    package: {
      bash: "=~5.1"
    }
  }

  command: "echo Hello"
}
```

### Running the package

Now that you've successfully created a package, let's run it in a new environment. Create a new environment using the
new `echo` package.

```shell
dagger new staging -p github.com/tjovicic/echo
```

Run it:

```shell
dagger up -e staging
```

At the end of the output you should see `Hello` printed out:

```shell
11:09AM INF system | starting buildkit    version=v0.8.3
WARN[0003] commandConn.CloseWrite: commandconn: failed to wait: signal: terminated 
WARN[0003] commandConn.CloseRead: commandconn: failed to wait: signal: terminated 
WARN[0003] commandConn.CloseWrite: commandconn: failed to wait: signal: terminated 
11:09AM INF ctr | computing
11:09AM INF ctr.#up[0].from | #5 0.080 fetch https://dl-cdn.alpinelinux.org/alpine/v3.13/main/x86_64/APKINDEX.tar.gz
11:09AM INF ctr.#up[0].from | #5 0.612 fetch https://dl-cdn.alpinelinux.org/alpine/v3.13/community/x86_64/APKINDEX.tar.gz
11:09AM INF ctr.#up[0].from | #5 1.094 (1/4) Installing ncurses-terminfo-base (6.2_p20210109-r0)
11:09AM INF ctr.#up[0].from | #5 1.149 (2/4) Installing ncurses-libs (6.2_p20210109-r0)
11:09AM INF ctr.#up[0].from | #5 1.273 (3/4) Installing readline (8.1.0-r0)
11:09AM INF ctr.#up[0].from | #5 1.361 (4/4) Installing bash (5.1.0-r0)
11:09AM INF ctr.#up[0].from | #5 1.534 Executing bash-5.1.0-r0.post-install
11:09AM INF ctr.#up[0].from | #5 1.541 Executing busybox-1.32.1-r6.trigger
11:09AM INF ctr.#up[0].from | #5 1.554 OK: 8 MiB in 18 packages
11:09AM INF ctr | #6 0.110 Hello
11:09AM INF ctr | completed    duration=5.4s
Output  Value  Description
```

## Manually distributing packages

You've probably guessed this package isn't tied to just your workspace. You can easily copy/paste it into any number
of different workspaces and use it as we've showed above.

```shell
mkdir -p /my-new-workspace/cue.mod/pkg/github.com/tjovicic/echo 
cp ./cue.mod/pkg/github.com/tjovicic/echo/source.cue /my-new-workspace/cue.mod/pkg/github.com/tjovicic/echo
```

## Contributing to Dagger stdlib

Our [stdlib](https://github.com/dagger/dagger/tree/main/stdlib) has many useful packages that you can use.
You've probably seen it when you've initialized your workspace:

```shell
.
├── cue.mod
│   ├── module.cue
│   ├── pkg
│   │   ├── alpha.dagger.io
│   │   └── .gitignore
│   └── usr
```

We are still a small community and are constantly looking for new contributors that will work with us improve this
amazing project. If you feel like we are missing a package or want to improve an existing one, please start with our
[contributing docs](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md) and open a PR.
