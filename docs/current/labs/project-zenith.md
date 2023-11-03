---
slug: /labs/project-zenith
title: "Project Zenith"
displayed_sidebar: "labs"
---

<head>
  <meta name="robots" content="noindex" />
</head>

:::danger
This is a short-lived page documenting a future release of Dagger. This release
is currently experimental and should not be considered production-ready. If you
arrived at this page by accident, you can [return to the official
documentation](../index.md).
:::

## Overview

_Project Zenith_ is the codename of a future release of Dagger, currently in
development. Certain features of Project Zenith are available in Dagger v0.9
with more coming rapidly.

The goal of the project is to make Dagger more accessible, by delivering it as
a CLI tool rather than just a library.

Features of Project Zenith include:

- Major expansion of the `dagger` CLI, removing the need to create a custom CLI
  for each project.
- Major expansion of the Dagger API, with a complete cross-language extension
  and composition system.
- An open ecosystem of reusable content, to take advantage of the extension and
  composition system called the [Daggerverse](https://daggerverse.dev/).

## How to get involved

The Dagger Engine is developed in the open, and Project Zenith is no exception.

- Discussions take place [on the Dagger Discord](https://discord.gg/dagger-io)
  in the `#project-zenith` channel. We love to hear from you, and there are no
  stupid questions!
- Contributors and testers meet every Friday at 09:00 Pacific time [on our
  Discord audio room](https://discord.com/channels/707636530424053791/911305510882513037).

If you get stuck, check out the [Troubleshooting guide](#troubleshooting) below.

## How to use it

Pre-requisites:

- A shell (bash, zsh, etc)
- [Docker](https://docs.docker.com/engine/install/)

### Downloading an experimental build

Since Dagger version v0.9 ships with Project Zenith functionality enabled, you can simply install the latest v0.9.x release of the Dagger CLI using [the install docs](https://docs.dagger.io/cli/465058/install).

:::note
Certain experimental Project Zenith features like `dagger call`, `dagger shell`, and `dagger up` are not shown in top level help output, but have help text under `dagger help call` and `dagger help shell`, etc.
:::

## Creating your first Module

Create a new directory on your filesystem and run `dagger mod init` to
bootstrap your first module. We'll call it `potato` here, but you can choose
your favorite food.

```sh
mkdir potato/
cd potato/

# initialize Dagger module
# NOTE: Node.js modules are not yet available, but under development.
dagger mod init --name=potato --sdk=go
```

This will generate a `dagger.json` module file, an initial `main.go`
source file, as well as a generated `dagger.gen.go` and `internal` folder for
the generated module code.

If you like, you can run the generated `main.go` like so:

```sh
dagger call container-echo --string-arg 'Hello daggernauts!'
```

or

```sh
echo '{potato{containerEcho(stringArg:"Hello daggernauts!"){stdout}}}' | dagger query
```

:::note
When using `dagger call` to call module functions, do not explicitly use the name of the local or remote module.
:::

:::note
When using `dagger call`, all names (functions, arguments, struct fields, etc) are converted into a shell-friendly "kebab-case" style.

When using `dagger query` and GraphQL, all names are converted into a language-agnostic "camelCase" style.
:::

Let's try changing the `main.go`. We named our module `potato`, so that means
all methods on the `Potato` type are published as functions. Let's replace the
template with something simpler:

```go
package main

type Potato struct{}

func (m *Potato) HelloWorld() string {
  return "Hello daggernauts!"
}
```

Next, run `dagger mod sync`. **You will need to run this command after every
change to your module's interface** (e.g. when you add/remove functions or
change their parameters and return types).

:::note
Module functions are flexible in what parameters they can take. You can include
an optional `context.Context`, and an optional `error` result. These are all
valid variations of the above:

```go
func (m *Potato) HelloWorld() string
func (m *Potato) HelloWorld() (string, error)
func (m *Potato) HelloWorld(ctx context.Context) string
func (m *Potato) HelloWorld(ctx context.Context) (string, error)
```

:::

To run the new function, once again use `dagger call` or `dagger query`:

```sh
dagger call hello-world
```

or

```sh
echo '{potato{helloWorld}}' | dagger query
```

Your functions can accept and return multiple different types, not just basic
builtin types. For example, to take multiple parameters (some of which can be
optional):

```go
package main

import "fmt"

type Potato struct{}

func (m *Potato) HelloWorld(
  // the number of potatoes to process
  count  int,
  // whether the potatoes are mashed (this is an optional parameter!)
  mashed Optional[bool],
) string {
  if mashed.GetOr(false) {
    return fmt.Sprintf("Hello world, I have mashed %d potatoes", count)
  }
  return fmt.Sprintf("Hello world, I have %d potatoes", count)
}
```

:::note
Use `--help` at the end of `dagger call` to get help on the commands and flags available.
:::

These options can then be set using `dagger call` or `dagger query` (exactly as if they'd been specified as top-level options):

```sh
dagger call hello-world --count 10 --mashed true
```

or

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
dagger call hello-world --message "I am a potato" message
dagger call hello-world --message "I am a potato" from
```

or

```sh
echo '{potato{helloWorld(message: "I am a potato"){message, from}}}' | dagger query
```

## More things you can do

### Call other modules

Modules can call each other! To add a dependency to your module, you can run
`dagger mod install`:

```sh
dagger mod install github.com/shykes/daggerverse/helloWorld@26f8ed9f1748ec8c9345281add850fd392441990
```

This module will be added to your `dagger.json`:

```json
  "dependencies": [
    "github.com/shykes/daggerverse/helloWorld@26f8ed9f1748ec8c9345281add850fd392441990"
  ]
```

You can also install local modules as dependencies. However, they must be stored in
a sub-directory of your module. For example:

```sh
dagger mod install ./path/to/module
```

The module will be added to your codegeneration, so you can access it from your
own module's code:

```go
func (m *Potato) HelloWorld(ctx context.Context) (string, error) {
  return dag.HelloWorld().Message(ctx)
}
```

You can find other modules to install on <https://daggerverse.dev>.

#### Module locations

You can consume modules from lots of different sources. The easiest way to
`dagger install`, `dagger call`, or `dagger query` a module is to reference it by its GitHub URL
(similar to Go package strings).

For example:

```sh
dagger call -m "github.com/user/repo@main" test
```

or

```sh
dagger query -m "github.com/user/repo@main" <<EOF
query test {
   ...
}
EOF
```

or, if your module is in a subdirectory of the Git repository:

```sh
dagger call -m "github.com/user/repo/subdirectory@main" test
```

or

```sh
dagger query -m "github.com/user/repo/subdirectory@main" <<EOF
query test {
   ...
}
EOF
```

You can also use modules from the local disk, without needing to push them to GitHub!

```sh
dagger call -m "./path/to/module" test
```

or

```sh
dagger query -m "./path/to/module" <<EOF
query test {
   ...
}
EOF
```

#### Publishing your own modules

You can publish your own modules to the
[Daggerverse](https://daggerverse.dev), so that other users can easily
discover them. At the moment, the Daggerverse is only used to discover other
modules, all the data is stored and fetched from GitHub.

To publish a module, create a Git repository for it and push to GitHub:

```sh
# assuming your module is in "potato/"
git init
git add potato/
git commit -m "Initial commit"

git remote add origin git@github.com:<user>/daggerverse.git
git push origin main
```

Next, navigate to <https://daggerverse.dev>, and use the top-module bar to
paste the GitHub link to your module (`github.com/<user>/daggerverse.git`),
then click "Crawl".

:::note
You don't _have_ to use `daggerverse` as the name of your Git repository -- it's just
a handy way to have all your modules in one Git repository together. But you
can always split them out into separate repositories, or name it something
different if you like!
:::

### Chaining

As mentioned above, your functions can return custom objects, which in turn can
define new functions! This allows for "chaining" of functions in the same style
as the core Dagger API.

As long as your object can be JSON-serialized by your SDK, its state will be
preserved and passed to the next function in the chain.

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

## Known issues

- A module's public fields require a `json:"foo"` tag to be queriable.
- When referencing another module as a local dependency, the dependent module
  must be stored in a sub-directory of the parent module.
- Custom struct types cannot currently be used as parameters.
- Calls to functions across modules will be run exactly _once_ per-session --
  after that, the result will be cached, but only until the next session (a new
  `dagger query`, etc).
  - At some point, we will add more fine-grained cache-control.
- Currently, Go and Python are the only supported languages for module development.
  - Python module development is not yet on par with Go.
  - Node.js modules are not yet available, but under development.

## Tips and tricks

- The context and error return are optional in the module's function signature;
  remove them if you don't need them.
- A module's private fields will not be persisted.

## Troubleshooting

Zenith still isn't complete! So, if you come across bugs, it helps to
have some techniques for working out what's going on.

If you run into problems, please share in the `#zenith-help` channel in the
[Dagger Discord](https://discord.gg/dagger-io)!

### Rerun commands with `--focus=false`

Sometimes, the Dagger client logs are automatically collapsed and don't contain
all the information from a failure.

To make sure that logs aren't automatically collapsed, you can run any `dagger`
subcommand with the `--focus=false` flag to disable this behavior.

### Access the `docker logs`

The Dagger Engine runs in a dedicated container. You can find the container:

```shell
DAGGER_ENGINE_DOCKER_CONTAINER="$(docker container list --all --filter 'name=^dagger-engine-*' --format '{{.Names}}')"
```

You can then access the logs for the container:

```shell
docker logs $DAGGER_ENGINE_DOCKER_CONTAINER
```
