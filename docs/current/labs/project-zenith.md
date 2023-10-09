---
slug: /labs/project-zenith
title: "Project Zenith"
displayed_sidebar: "labs"
---

<head>
  <meta name="robots" content="noindex" />
</head>

:::danger
This is a short-lived page documenting a future release of Dagger. This release is currently experimental and should not be considered production-ready. If you arrived at this page by accident, you can [return to the official documentation](../index.md).
:::

## Overview

*Project Zenith* is the codename of a future release of Dagger, currently in development (and hopefully released soon!)

The goal of the project is to make Dagger more accessible, by delivering it as a CLI tool rather than just a library.

Features of Project Zenith include:

* Major expansion of the `dagger` CLI, removing the need to create a custom CLI for each project.
* Major expansion of the Dagger API, with a complete cross-language extension and composition system.
* An open ecosystem of reusable content, to take advantage of the extension and composition system called the [Daggerverse](https://daggerverse.fly.dev/).

## How to get involved

The Dagger Engine is developed in the open, and Project Zenith is no exception.

* Discussions take place [on the Dagger Discord](https://discord.gg/dagger-io) in the `#project-zenith` channel. We love to hear from you, and there are no stupid questions!
* Contributors and testers meet every Friday at 09:00 Pacific time [on our Discord audio room](https://discord.com/channels/707636530424053791/911305510882513037).

If you get stuck, check out the [Troubleshooting guide](#troubleshooting) below.

## How to use it

Pre-requisites:

- A shell (bash, zsh, etc)
- [Docker](https://docs.docker.com/engine/install/)

### Downloading an experimental build

You can download an experimental build of dagger from [github.com/jpadams/shykes-dagger-zenith-builder](https://github.com/jpadams/shykes-dagger-zenith-builder/releases/tag/nightly).

Select the right build using your OS (darwin or linux) and platform (amd64 or arm64), and move it to a directory within your path, for example:

```sh
# create a personal bin directory and add it to the PATH
mkdir -p ~/bin
export PATH=$PATH:~/bin

# install the downloaded binary
mv ~/Downloads/dagger-zenith-linux-amd64 ~/bin/dagger
```

You should then be able to use the `dagger` command:

```sh
dagger version
# dagger devel () (jeremyatdockerhub/dagger-engine-worker-zenith) linux/amd64
```

You can also run a quick hello world to check everything's working:

```sh
dagger query <<EOF
{
  container {
    from(address:"alpine") {
      withExec(args:["echo", "hello daggernauts!"]) {
        stdout
      }
    }
  }
}
EOF
```

### Building from scratch (advanced)

<details>
<summary>Building from scratch instructions</summary>

To get started, clone or pull this branch:

```sh
# fresh clone
git clone https://github.com/shykes/dagger --branch zenith-functions ./dagger/

# OR pull branch
git remote add shykes https://github.com/shykes/dagger
git fetch shykes zenith-functions
git checkout zenith-functions
```

Next, build the dev `dagger` CLI and start the dev engine:

```sh
# cd into repo
cd ./dagger/

# build dev CLI and engine
./hack/dev
```

Finally, you need to configure the dagger environment variables to point to the running engine.

1. If you use [direnv](https://direnv.net/), you can just:

    ```sh
    cd ./zenith
    direnv allow .
    ```

2. If not, you can directly `source` the provided `.envrc` file:

    ```sh
    cd ./zenith
    source .envrc
    ```

At this point you should have a fully functioning `dagger` CLI and dev engine.

You should then be able to use the `dagger` command:

```sh
./bin/dagger version
# dagger devel () (registry.dagger.io/engine) linux/amd64
```

You can also run a quick hello world to check everything's working:

```sh
./bin/dagger query <<EOF
{
  container {
    from(address:"alpine") {
      withExec(args:["echo", "hello daggernauts!"]) {
        stdout
      }
    }
  }
}
EOF
```

</details>

## Creating your first Module

Create a new directory on your filesystem and run `dagger mod init` to
bootstrap your first module. We'll call it `potato` here, but you can choose
your favorite food.

```sh
mkdir potato/
cd potato/

# initialize Dagger module
# NOTE: currently Go is the only supported SDK.
dagger mod init --name=potato --sdk=go
```

This will generate a `dagger.json` module file, an initial `main.go`
source file, as well as a generated `dagger.gen.go` and `internal` folder for the generated module code.

If you like, you can run the generated `main.go` like so:

```sh
echo '{potato{myFunction(stringArg:"Hello daggernauts!"){id}}}' | dagger query
```

> **Note**
>
> All names (functions, arguments, struct fields), etc, are converted into a langauge-agnostic camel-case style.

Let's try changing the `main.go`. We named our module `potato`, so that means all
methods on the `Potato` type are published as functions. Let's replace the
template with something simpler:

```go
package main

type Potato struct{}

func (m *Potato) HelloWorld() string {
	return "Hello daggernauts!"
}
```

Next, run `dagger mod sync`. **You will need to run this command after every
change to your module's interface.**

> **Note**
> Module functions are flexible in what parameters they can take. You can include an optional `context.Context`, and an optional `error` result. These are all valid variations of the above:
> ```go
> func (m *Potato) HelloWorld() string
> func (m *Potato) HelloWorld() (string, error)
> func (m *Potato) HelloWorld(ctx context.Context) string
> func (m *Potato) HelloWorld(ctx context.Context) (string, error)
> ```

To run the new function, once again use `dagger query`:

```sh
echo '{potato{helloWorld}}' | dagger query
```

Your functions can accept and return multiple different types, not just basic builtin types. For example, to take an object (which you can use to provide optional parameters, or to group large numbers of parameters together):

```go
package main

import "fmt"

type Potato struct{}

type PotatoOptions struct {
	Count  int
	Mashed bool
}

func (m *Potato) HelloWorld(opts PotatoOptions) string {
	if opts.Mashed {
		return fmt.Sprintf("Hello world, I have mashed %d potatoes", opts.Count)
	}
	return fmt.Sprintf("Hello world, I have %d potatoes", opts.Count)
}
```

These options can then be set using `dagger query` (exactly as if they'd been specified as top-level options):

```sh
echo '{potato{helloWorld(count:10, mashed:true)}}' | dagger query
```

Or, to return a custom type:

```go
package main

type Potato struct{}

// HACK: to be queried, custom object fields require `json` tags
type PotatoMessage struct {
	Message string `json:"message"`
	From    string `json:"from"`
}

// HACK: this is temporarily required to ensure that the codegen discovers
// PotatoMessage
func (msg PotatoMessage) Void() {}

func (m *Potato) HelloWorld(message string) PotatoMessage {
	return PotatoMessage{
		Message: message,
		From:    "potato@example.com",
	}
}
```

```sh
echo '{potato{helloWorld(message: "I'm a potato!"){message, from}}}' | dagger query
```

> **TODO(jedevc)**
>
> Do structs nest?

> **TODO(jedevc)**
>
> Pointer options don't work

## More things you can do

### Call other modules

Modules can call each other!

> **TODO**
>
> Find a module to use as an example here

To add a dependency to your module, you can use `dagger mod use`:

```sh
dagger mod use github.com/.../.../...
```

This module will be added to your `dagger.json`:

```json
  "dependencies": [
    "github.com/.../.../...@22596363b3de40b06f981fb85d82312e8c0ed511"
  ]
```

You can also use local modules as dependencies. However, they must be stored in a sub-directory of your module. For example:

```sh
dagger mod use some_other_module
```

The module can be called using `dagger query`:

```sh
echo '{...}' | dagger query
```

It'll also be added to your codegeneration, so you can access it from your own module's code:

```go
func (m *Potato) HelloWorld(ctx context.Context) (string, error) {
	// ...
}
```

You can find other modules to use on <https://daggerverse.fly.dev>.

> **TODO(jedevc)**
>
> Are multiple module dependencies supported?

#### Module locations

You can consume modules from lots of different sources. The easiest way to `dagger use` or `dagger query` module is to reference it by it's GitHub URL (similar to go package strings).

For example:

```sh
dagger query -m "github.com/user/repo@main" <<EOF
query test {
   ...
}
EOF
```

or, if your module is in a subdirectory of the Git repository:

```sh
dagger query -m "github.com/user/repo/subdirectory@main" <<EOF
query test {
   ...
}
EOF
```

You can also use modules from the local disk, without needing to push them to GitHub!

```sh
dagger query -m "./path/to/module" <<EOF
query test {
   ...
}
EOF
```

#### Publishing your own modules

You can publish your own modules to <https://daggerverse.fly.dev>, so that other users can easily discover them. At the moment, daggerverse is only used to discover other modules, all the data is stored and fetched from GitHub.

To publish a module, create a git repository for it and push to GitHub:

```sh
# assuming your module is in "potato/"
git init
git add potato/
git commit -m "Initial commit"

git remote add origin git@github.com:<user>/daggerverse.git
git push origin main
```

Next, navigate to <https://daggerverse.fly.dev>, and use the top-module bar to paste the GitHub link to your module (`github.com/<user>/daggerverse.git`), then click "Crawl".

> **Note**
>
> You don't *have* to use `daggerverse` as the name of your git repo -- it's just a handy way to have all your modules in one git repository together. But you can always split them out into separate repositories, or name it something different if you like!

> **TODO(jedevc)**
>
> Are multiple module dependencies supported?

### Chaining

As mentioned above, your functions can return custom objects, which in turn can define new functions! This allows for "chaining" of functions in the same style as the core Dagger API.

As long as your object can be JSON-serialized by your SDK, its state will be preserved and passed to the next function in the chain.

Here is an example module using the Go SDK:

```go
// A dagger module for saying hello world!

package main

import (
	"context"
	"fmt"
)

type HelloWorld struct {
	Greeting string
	Name     string
}

func (hello *HelloWorld) WithGreeting(ctx context.Context, greeting string) (*HelloWorld, error) {
	hello.Greeting = greeting
	return hello, nil
}

func (hello *HelloWorld) WithName(ctx context.Context, name string) (*HelloWorld, error) {
	hello.Name = name
	return hello, nil
}

func (hello *HelloWorld) Message(ctx context.Context) (string, error) {
	var (
		greeting = hello.Greeting
		name     = hello.Name
	)
	if greeting == "" {
		greeting = "Hello"
	}
	if name == "" {
		name = "World"
	}
	return fmt.Sprintf("%s, %s!", greeting, name), nil
}
```

And here is an example query for this module:

```graphql
{
  helloWorld {
    message
    withName(name: "Monde") {
      withGreeting(greeting: "Bonjour") {
        message
      }
    }
  }
}
```

The result will be:

```json
{
  "helloWorld": {
    "message": "Hello, World!",
    "withName": {
      "withGreeting": {
        "message": "Bonjour, Monde!"
      }
    }
  }
}
```

### Extend core types

You can add a new function to accept and return a `*Container`.

```go
package main

type Potato struct{}

func (c *Container) AddPotato() *Container {
	return c.WithNewFile("/potato", ContainerWithNewFileOpts{
		Contents: "i'm a potato",
	})
}
```

Next, run `dagger mod sync`.

To run the new function, once again use `dagger query` (this example requires a Snyk token):

```sh
dagger query <<EOF
{
  container {
    from(address:"alpine") {
      addPotato {
        withExec(args:["cat", "potato"]) {
          stdout
        }
      }
    }
  }
}
EOF
```

## Known issues

- A module's public fields require a `json:"foo"` tag to be queriable.
- Custom objects in a module require at least one method to be defined on them to be detected by the codegen.
- When referencing another module as a local dependency, the dependent module must be stored in a sub-directory of the parent module.
- Custom struct types used as parameters cannot be nested and contain other structs themselves.
- Calls to functions across modules will be run exactly *once* per-session -- after that, the result will be cached, but only until the next session (a new `dagger query`, etc).
  - At some point, we will add more fine-grained cache-control.

## Tips and tricks

- The context and error return are optional in the module's function signature; remove them if you don't need them.
- A module's private fields will not be persisted.

## Troubleshooting

Zenith still isn't perfectly complete! So, if you come across bugs, it helps to have some techniques for working out what's going on.

If you run into problems, please share in the `#zenith-help` channel in the
[Dagger Discord](https://discord.gg/dagger-io)!

### Rerun commands with `--focus=false`

Sometimes, the dagger client logs are automatically collapsed and don't contain all the information from a failure.

To make sure that logs aren't automatically collapsed, you can run any `dagger` subcommand with the `--focus=false` flag to disable this behavior.

### Access the `docker logs`

The dagger engine runs in a dedicated container. You can find the container using `docker ps`:

```
CONTAINER ID   IMAGE                                                COMMAND                  CREATED       STATUS       PORTS     NAMES
ffdef3982445   jeremyatdockerhub/dagger-engine-worker-zenith:main   "dagger-entrypoint.s…"   2 hours ago   Up 2 hours             dagger-engine-272478830461fcff
```

You can then access the logs for the container:

```
# replace <container-id> with the found id from above
docker logs <container-id>
```
