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

# initialize Dagger module
#
# NOTE: currently Go is the only supported SDK.
dagger mod init --name=vito --sdk=go
```

This will generate `dagger.gen.go`, `dagger.json`, and an initial `main.go`
file.

If you like, you can run the generated `main.go` like so:

```sh
echo '{vito{myFunction(stringArg:"hey"){id}}}' | dagger query
```

Let's try changing the `main.go`. We named our module `vito`, so that means all
methods on the `Vito` type are published as functions. Let's replace the
template with something simpler:

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

To run the new function, once again use `dagger query`:

```sh
echo '{vito{helloWorld}}' | dagger query
```

That's it! ...For now.

## More things you can do

You can add a new function to accept and return a `*Container`.

```go
package main

import "context"

type Vito struct{}

func (m *Vito) HelloWorld(context.Context) (string, error) {
	return "hey", nil
}

func (m *Vito) Snyk(ctx context.Context, ctr *Container) (*Container, error) {
	return ctr, nil
}

func (ctr *Container) Snyk(ctx context.Context, token string, path string) (*Container, error) {
	c := ctr.
		WithWorkdir("/tmp").
		WithExec([]string{"curl", "https://static.snyk.io/cli/latest/snyk-alpine", "-o", "snyk"}).
		WithExec([]string{"chmod", "+x", "snyk"}).
		WithExec([]string{"mv", "./snyk", "/usr/local/bin"}).
		WithWorkdir(path).
		WithEnvVariable("SNYK_TOKEN", token).
		WithExec([]string{"snyk", "test"})

	return c, nil
}
```

Next, run `dagger mod sync`.

To run the new function, once again use `dagger query` (this example requires a Snyk token):

```sh
dagger query  << EOF
query test {
		container {
				from(address: "alpine") {
						withExec(args: ["apk", "add", "curl"]) {
								withExec(args: ["apk", "add", "git"]) {
										withExec(args: ["git", "clone", "https://github.com/snyk/snyk-demo-todo.git", "/src"]) {
												snyk(token: "TOKEN", path: "/src") {
														stdout
												}
										}
								}
						}
				}
		}
}
EOF
```

If you push your module to a Git repository, you can reference it using `git://`.

```sh
dagger query -m "git://github.com/user/repo?ref=main" << EOF
query test {
   ...
}
EOF
```

TODO: flesh this out

* Return custom types with methods defined on them

## Questions?

Please ask questions and share feedback in the `#project-zenith` channel in the
[Dagger Discord](https://discord.gg/dagger-io). We love to hear from you, and
there are no stupid questions!

Thanks and happy testing.
