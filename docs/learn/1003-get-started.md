---
slug: /1003/get-started/
---

# Get Started with Dagger

In this tutorial, you will learn the basics of Dagger by building a Dagger project from scratch. This simple project deploys a [React](https://reactjs.org/) application to your local machine via Docker. In later tutorials, you will learn how to configure Dagger to deploy to remote infrastructure such as EKS and GKE.

This tutorial does involve writing CUE, so if you haven&rsquo;t already, be sure to read [What is CUE?](./1005-what_is_cue.md)

In this tutorial we will learn:

- How to initialize and structure a Dagger project
- About Dagger concepts such as
  - the plan
  - environments
  - inputs and outputs
- How to write CUE for Dagger
- How to deploy an application with Dagger

## Deploy an Application Locally

The following instructions assume you are working locally, but could just as easily be run on a remote machine into which you have a shell.

### Install Dagger

First, make sure [you have installed Dagger](../1001-install.md). You can run `dagger version` to ensure you have the latest installed and working.

### Create a Dagger Project

First clone the [Dagger examples repository](https://github.com/dagger/examples), change directories to the `todoapp/` and list its contents:

> Note that all tutorials will operate from the todoapp directory.

```bash
git clone https://github.com/dagger/examples.git
cd examples/todoapp
ls -la
```

This React application will use Yarn to build a static website with the following directories and files.

```bash
-rw-r--r--   ...     794 Sep  7 10:09 package.json
drwxr-xr-x   ...     256 Sep  7 10:09 public
drwxr-xr-x   ...     192 Sep 29 11:17 src
-rw-r--r--   ...  465514 Sep 29 11:17 yarn.lock
```

Now we need to initialize this directory as a Dagger _project_:

```bash
dagger init
ls -la
```

You will now see 2 new directories:

- The `.dagger` directory will store metadata about _environments_, _inputs_, and _outputs_ which we will cover later.
- The `cue.mod` directory stores libraries such as [dagger/universe](https://github.com/dagger/universe) which can be _imported_ into your Dagger plan.

Dagger will load all `.cue` files recursively in the current Dagger project. More directories can be added to help organize code.

> Note that Dagger, like the CUE CLI command, will only load CUE files from the `cue.mod` directory in response to `import` statements.

### Write a Dagger Plan

A Dagger _plan_ is written in CUE and defines the _resources_, _dependencies_, and _logic_ to deploy an application to an environment. Unlike traditional glue code written in a scripting language such as Bash or PowerShell, a Dagger plan is _declarative_ rather than _imperative_. This frees us from thinking about the order of operations, since Dagger will infer dependendencies and calculate correct order on its own.

Let&rsquo;s first create a directory to hold our Dagger plan separately from our application code:

```bash
mkdir -p ./plans/local
```

We will now create the following files:

- `plans/todoapp.cue` which will define resources common to all environments
- `plans/local/local.cue` which will define resources specific to the local environment

Create the file `plans/todoapp.cue` with the following content:

```cue file=./tests/getting-started/plans/todoapp.cue
```

This file will define the resources and relationships between them that are common across _all environments_. For example, here we are deploying to our local Docker engine in our `local` environment, but for staging or production as examples, we would deploy the same image to some other container orchestration system such as Kubernetes hosted somewhere out there among the various cloud providers.

Create the file `plans/local/local.cue` with the following content:

```cue file=./tests/getting-started/plans/local/local.cue

```

Notice that both files have the same `package todoapp` declared on the first line. This is crucial to inform CUE that they are to be loaded and evaluated together in the same context.

Our `local.cue` file now holds resources specific to our `local` environment. Also notice that we are defining a concrete value for the `target` key here. The entire `push` object is defined in both files and CUE will merge the values into a single struct with key:value pairs that are _complete_ with concrete values.

### Create an Environment

Before we can deploy the plan, we need to define an environment which is the specific plan to execute, as well as the context from which inputs are pulled and to which state is stored.

For our each environment we need to tell Dagger what CUE files to load, so let&rsquo;s create a `local` environment:

```shell
dagger new local -p ./plans/local
dagger list
```

The `list` command shows the current environments defined:

```bash
local ...todoapp/.dagger/env/local
```

### Define Input Values per Environment

Our Dagger plan includes a number of references to `dagger.#Input` which inform the Dagger engine that the concrete value should be pulled from inputs at runtime. While some values such as the registry target we saw above can be expressed purely in CUE, others such as directories, secrets, and sockets are required to be explicitly defined as _inputs_ to protect against malicious code being injected by third-party packages. If Dagger allowed such things to be stated in CUE, the entire package system could become a source of attacks.

List the inputs Dagger is aware of according to our plan:

```shell
dagger -e local input list
```

You should see the following output:

```bash
Input       Value             Set by user  Description
app.source  dagger.#Artifact  false        Application source code
run.socket  struct            false        Mount local docker socket
```

Notice that `Set by user` is _false_ for both, because we have not yet provided Dagger with those values.

Let&rsquo;s provide them now:

```shell
dagger -e local input socket run.socket /var/run/docker.sock
dagger -e local input dir app.source ./

```

This defines the `run.socket` as a `socket` input type, and the `app.source` input as a `dir` input type.

Now let&rsquo;s replay the `dagger input list` command:

```bash
Input       Value             Set by user  Description
app.source  dagger.#Artifact  true         Application source code
run.socket  struct            true         Mount local docker socket
```

Notice that Dagger now reports that both inputs have been set.

### Deploy the Appplication

With our plan in place, our environment set, and our inputs defined, we can deploy the application as simply as:

```bash
dagger up
```

Once complete you should get logs, and a final output like this:

```bash
Output                 Value                                          Description
app.build              struct                                         Build output directory
push.ref               "localhost:5000/todoapp:latest@sha256:<hash>"  Image ref
push.digest            "sha256:<hash>"                                Image digest
run.ref                "localhost:5000/todoapp:latest@sha256:<hash>"  Image reference (e.g: nginx:alpine)
run.run.env.IMAGE_REF  "localhost:5000/todoapp:latest@sha256:<hash>"  -
```

Congratulations! You&rsquo;ve deployed your first Dagger plan! You can now [view the todo app](http://localhost:8080) in your browser!
