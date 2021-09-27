---
slug: /1003/get-started/
---

# Get Started with Dagger

In this tutorial, you will learn the basics of Dagger by building a Dagger project from scratch. This simple
project deploys a [React](https://reactjs.org/) application to your local machine via docker. In later tutorials,
you will learn how to configure Dagger to deploy to your infrastructure. And, for advanced users,
how to share access to your infrastructure in the same way that we share access to ours now.

This tutorial does involve writing CUE, so if you haven&rsquo;t already, be sure to read [What is CUE?](../introduction/1005-what_is_cue.md)

In this tutorial we will learn:

- How to initialize and structure a Dagger project
- About Dagger concepts such as
  - the plan
  - environments
  - inputs and outputs
- How to write CUE for Dagger
- How to deploy an application using `dagger up`

## Deploy an Application Locally

The following instructions assume you are working locally, but could just as easily be run on a remote
machine into which you have a shell. For the sake of brevity and simplicity we will create directories under
your home directory, but feel free to replace `~/` with a path that works best for you.

### Install Dagger

First, make sure [you have installed Dagger](../1001-install.md). You can run `dagger version` to ensure
you have the latest installed and working. 

### Create a Dagger Project

First we need a directory that will contain our `.cue` files and a `.dagger` directory which stores metadata about environments. First, create a new directory for our todoapp, then initialize the project:

```bash
mkdir ~/todoapp
cd ~/todoapp
dagger init
```

If you now run `ls -la` you will see 2 new directories:

- The `.dagger` directory will store metadata about _environments_, _inputs_, and _outputs_ which we will cover shortly.
- The `cue.mod` directory stores libraries such as [dagger/universe](https://github.com/dagger/universe) which can be _imported_ into your Dagger _plan_.

Dagger will load all `.cue` files recursively in the current Dagger project. More directories can be added to help organize code.

> Note that Dagger, like the CUE CLI command, will only load CUE files from the `cue.mod` directory in response to `import` statements.

### Write a Dagger Plan

A Dagger _plan_ is written in CUE and expresses the _resources_, _dependencies_, and _logic_ to deploy an application to an environment. Unlike traditional glue code written in an scripting language (e.g.: Bash, PowerShell), a Dagger plan is _declarative_ rather than _imperative_. This frees us from thinking about order of operations, since Dagger will infer dependendencies and calculate correct order on its own.

First create a directory to hold our plan, separate from our application code:

```shell
mkdir ./plan
```

Next, create a file in `plan/` called `todoapp.cue` with the following content

```cue
package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/stream"
	"alpha.dagger.io/js/yarn"
)

// Source code of the sample application
source: dagger.#Artifact & dagger.#Input

// Build the source code using Yarn
app: yarn.#Package & {
	"source": source
}

```

### Create an Environment

```shell
dagger new local -p ./plan
dagger list
```

### Define Input Values per Environment

```shell
dagger input list
```

```text
Input       Value             Set by user  Description
app.source  dagger.#Artifact  false        Application source code
```

```shell
dagger -e local input dir app.source ./app 
```

