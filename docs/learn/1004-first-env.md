---
slug: /1004/dev-first-env/
---

# Create your first Dagger environment

## Overview

In this guide, you will create your first Dagger environment from scratch,
and use it to deploy a React application to two locations in parallel:
a dedicated [Amazon S3](https://wikipedia.org/wiki/Amazon_S3) bucket, and a
[Netlify](https://en.wikipedia.org/wiki/Netlify) site.

You will need to understand the [CUE language](https://cuelang.org), so be sure to read [What Is Cue?](../introduction/1005-what_is_cue.md) if you haven&rsquo;t already.

In technical terms, our plan is a [CUE Package](https://cuelang.org/docs/concepts/packages/#packages). This tutorial will develop a new CUE package from scratch for our plan, but you can use any Cue package as a plan.

### Anatomy of a Dagger environment

A Dagger environment contains all the code and data necessary to deliver a particular application in a specific way.
For example, the same application might be delivered to a production and staging environment, each with its own configuration.

An environment is made of 3 parts:

- A _plan_, authored by the environment's _developer_, using the [Cue](https://cuelang.org) language.

- _Inputs_, supplied by the environment's _user_ via the `dagger input` command and written to a particular file. Inputs may be configuration values, artifacts, or encrypted secrets.

- _Outputs_, computed by the Dagger engine via the `dagger up` command and recorded to a particular directory.

We will first develop our environment's _plan_, configure its initial inputs, then finally run it to verify that it works.

### Anatomy of a plan

A _plan_ specifies, in code, how to deliver a particular application in a specific way.
It is your environment's source code.

Unlike regular imperative programs, which specify a sequence of instructions to execute,
a Dagger plan is _declarative_: it lays out your application's supply chain as a graph
of interconnected nodes.

Each node in the graph represents a component of the supply chain, for example:

- Development tools: source control, CI, build systems, testing systems
- Hosting infrastructure: compute, storage, networking, databases, CDNs
- Software dependencies: operating systems, languages, libraries, frameworks, etc.

Each link in the graph represents a flow of data between nodes. For example:

- source code flows from a git repository to a build system
- system dependencies are combined in a docker image, then uploaded to a registry
- configuration files are generated then sent to a compute cluster or load balancer

## Initial setup

### Install Cue

Although not strictly necessary, for an optimal development experience, we recommend
[installing a recent version of Cue](https://github.com/cuelang/cue/releases/).

### Prepare Cue learning resources

If you are new to Cue, we recommend keeping the following resources in browser tabs:

- The unofficial but excellent [Cuetorials](https://cuetorials.com/overview/foundations/) in a browser tab, to look up Cue concepts as they appear.

- The official [Cue interactive sandbox](https://cuelang.org/play) for easy experimentation.

### Setup example app

You will need a local copy of the [Dagger examples repository](https://github.com/dagger/examples).
NOTE: you may use the same local copy across all tutorials.

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are run from the `todoapp` directory:

```shell
cd examples/todoapp
```

## Develop the plan

### Initialize a Cue module

Developing for Dagger takes place in a [Cue module](https://cuelang.org/docs/concepts/packages/#modules).
If you are familiar with Go, Cue modules are directly inspired by Go modules.
Otherwise, don't worry: a Cue module is simply a directory with one or more Cue packages in it. For example, a Cue module has a `cue.mod` directory at its root.

This guide will use the same directory as the root of the Dagger project and the Cue module, but you can create your Cue module anywhere inside the Dagger project. In general, you won't have to worry about it at all. You will initialize a dagger project with the following command.

```shell
dagger init # Optional, already present in `todoapp`
```

> In our case, `todoapp` already contains a `.dagger` directory, so this step is optional.

### Create a Cue package

Now we start developing our Cue package at the root of our Cue module.

In this guide, we will split our package into multiple files, one per component.
Thus, it is typical for a Cue package to have only one file. However, you can organize your package any way you want: the Cue evaluator merges all files from the same package, as long as they are in the same directory, and start with the same
`package` clause...
See the [Cue documentation](https://cuelang.org/docs/concepts/packages/#files-belonging-to-a-package) for more details.

We will call our package `multibucket` because it sounds badass and vaguely explains what it does.
But you can call your packages anything you want.

Let's create a new directory for our Cue package:

```shell
mkdir multibucket
```

### Component 1: app source code

The first component of our plan is the source code of our React application.

In Dagger terms, this component has two essential properties:

1. It is an _artifact_: something that can be represented as a directory.
2. It is an _input_: something that is provided by the end-user.

Let's write the corresponding Cue code to a new file in our package:

```cue file=./tests/multibucket/source.cue title="todoapp/cue.mod/multibucket/source.cue"

```

This code defines a component at the key `src` and specifies that it is both an artifact and an input.

### Component 2: yarn package

The second component of our plan is the Yarn package built from the app source code:

```cue file=./tests/multibucket/yarn.cue title="todoapp/cue.mod/multibucket/yarn.cue"

```

Let's break it down:

- `package multibucket`: this file is part of the multibucket package
- `import ( "alpha.dagger.io/js/yarn" )`: import a package from the [Dagger Universe](../reference/README.md).
- `app: yarn.#Package`: apply the `#Package` definition at the key `app`
- `&`: also merge the following values at the same key...
- `{ source: src }`: set the key `app.source` to the value of `src`. This snippet of code connects our two components, forming the first link in our DAG

### Component 3: dedicated S3 bucket

_FIXME_: this section is not yet available because the [Amazon S3 package](https://github.com/dagger/dagger/tree/main/stdlib/aws/s3) does [not yet support bucket creation](https://github.com/dagger/dagger/issues/623). We welcome external contributions :)

### Component 4: deploy to Netlify

The third component of our plan is the Netlify site to which the app will be deployed:

```cue file=./tests/multibucket/netlify.cue title="todoapp/multibucket/netlify.cue"

```

This component is very similar to the previous one:

- We use the same package name as the other files
- We import another package from the [Dagger Universe](../reference/README.md).
- `site: "netlify": site.#Netlify`: apply the `#Site` definition at the key `site.netlify`. Note the use of quotes to protect the key from name conflict.
- `&`: also merge the following values at the same key...
- `{ contents: app.build }`: set the key `site.netlify.contents` to the value of `app.build`. This line connects our components 2 and 3, forming the second link in our DAG.

### Exploring a package documentation

But wait: how did we know what fields were available in `yarn.#Package` and `netlify.#Site`?
Answer: thanks to the `dagger doc` command, which prints the documentation of any package from [Dagger Universe](../reference/README.md).

```shell
dagger doc alpha.dagger.io/netlify
dagger doc alpha.dagger.io/js/yarn
```

You can also browse the [Dagger Universe](../reference/README.md) reference in the documentation.

## Setup the environment

### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

```shell
dagger new 'multibucket' -p ./multibucket
```

### Configure user inputs

You can inspect the list of inputs (both required and optional) using dagger input list:

```shell
dagger input list -e multibucket
# Input                       Value              Set by user  Description
# site.netlify.account.name   *"" | string       false        Use this Netlify account name (also referred to as "team" in the Netlify docs)
# site.netlify.account.token  dagger.#Secret     false        Netlify authentication token
# site.netlify.name           string             false        Deploy to this Netlify site
# site.netlify.create         *true | bool       false        Create the Netlify site if it doesn't exist?
# src                         dagger.#Artifact   false        Source code of the sample application
# app.cwd                     *"." | string      false        working directory to use
# app.writeEnvFile            *"" | string       false        Write the contents of `environment` to this file, in the "envfile" format
# app.buildDir                *"build" | string  false        Read build output from this directory (path must be relative to working directory)
# app.script                  *"build" | string  false        Run this yarn script
# app.args                    *[] | []           false        Optional arguments for the script
```

All the values without default values (without `*`) have to be specified by the user. Here, required fields are:

- `site.netlify.account.token`, your access token
- `site.netlify.name`, name of the published website
- `src`, source code of the app

Please note the type of the user inputs: a string, a #Secret, and an artifact. Let's see how to input them:

```shell
# As a string input is expected for `site.netlify.name`, we set a `text` input
dagger input text site.netlify.name <GLOBALLY-UNIQUE-NAME> -e multibucket

# As a secret input is expected for `site.netlify.account.token`, we set a `secret` input
dagger input secret site.netlify.account.token <PERSONAL-ACCESS-TOKEN> -e multibucket

# As an Artifact is expected for `src`, we set a `dir` input (dagger input list for alternatives)
dagger input dir src . -e multibucket

```

### Deploy

Now that everything is appropriately set, let's deploy on Netlify:

```shell
dagger up -e multibucket
```

### Using the environment

[This section is not yet written](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md)

## Share your environment

### Introduction to gitops

[This section is not yet written](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md)

### Review changes

[This section is not yet written](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md)

### Commit changes

[This section is not yet written](https://github.com/dagger/dagger/blob/main/CONTRIBUTING.md)
