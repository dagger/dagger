# Project Zenith

## Overview

*Project Zenith* is the codename of a future release of Dagger, currently in development.

The goal of Project Zenith is to make Dagger more accessible, by delivering it as a CLI tool rather than just a library.

Features of Project Zenith include:

* Major expansion of the `dagger` CLI, removing the need to create a custom CLI for each project
* Major expansion of the Dagger API, with a complete cross-language extension and composition system
* An open ecosystem of reusable content, to take advantage of the extension and composition system
* A major overhaul to our documentation and marketing, to explain Dagger as a tool for development and CI, rather than "just a CI engine"

## Status

As of August 24 2023, Project Zenith is in active development, with the goal of releasing before the end the year.

## How to participate

The Dagger Engine is developed in the open, and Project Zenith is no exception.

* Discussions take place [on our Discord server](https://discord.com/channels/707636530424053791/1120503349599543376)
* Contributors and testers meet every friday at 09:00 Pacific time [on our Discord audio room](https://discord.com/channels/707636530424053791/911305510882513037)


## How to test it

To get started, first clone this branch to a separate directory.

> You could also just fetch the branch into an existing Dagger repo checkout,
> but I've found it nice to keep them separate, since it's a bit of a
> context-switch if you have anything else going on in your Dagger repo.

```sh
git clone https://github.com/shykes/dagger --branch zenith-functions ./zenith/
```

Next, `cd` to it and build the dev `dagger` CLI and start the dev engine:

```sh
# cd into repo
cd ./zenith/

# build dev CLI and engine
./hack/dev
```

Finally, `cd` one last time into the directory containing this README.md file.
It contains an [`.envrc`][direnv] file that will automatically point your
`dagger` CLI to the dev engine.

Follow the [install instructions][direnv] for `direnv` if you don't have it
already, and then run:

[direnv]: https://direnv.net/

```sh
# cd into zenith subdir
cd ./zenith/

# enable .envrc (be sure to start a new shell)
direnv allow
```

At this point you should have a fully functioning `dagger` CLI and dev engine.

You can test it by running the included demo module:

```sh
cd vito-mod/
echo '{vito{helloWorld}}' | dagger query --progress=plain
```

## Creating your first Module

Create a new directory under `./zenith/` and run `dagger mod init` to
bootstrap your first module.

```sh
cd ./zenith/ # if not there already

mkdir vito-mod/
cd vito-mod/

# initialize Go module
#
# TODO: this can be autoamted
go mod init vito-mod

# bootstrap go.mod/go.sum
#
# TODO: this can be autoamted, and should pin to appropriate dependencies
go mod tidy

# initialize Dagger module
#
# NOTE: currently Go is the only supported SDK.
dagger mod init --name=vito --sdk=go
```

This will generate `dagger.gen.go`, `dagger.json`, and an empty `main.go` file.

Let's write the `main.go` now. We named our module `vito`, so that means we
need to define a `Vito` type. This type will define all of the functions
available from our module.

```go
package main

import "context"

type Vito struct{}

func (m *Vito) HelloWorld(context.Context) (string, error) {
	return "hey", nil
}
```

> Currently all methods _must_ accept a `ctx` argument and include an `error`
> return value. These constraints will be lifted soon.

Next, run `dagger mod sync`. **You will need to run this command after every
change to your module code.** We will figure out how to automate it in the
future.

At this point you should have a fully functioning module. You can test it with
`dagger query`:

```sh
echo '{vito{helloWorld}}' | dagger query
```

## More things you can do

TODO: flesh this out

* Accept and return types like `*Container`
* Return custom types with methods defined on them

## Questions?

Please ask questions and share feedback in the `#project-zenith` channel in the
[Dagger Discord](https://discord.gg/dagger-io). We love to hear from you, and
there are no stupid questions!

Thanks and happy testing.
