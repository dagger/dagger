# Dagger Programmer Guide

## Overview

Dagger automates application delivery by executing *plans* in *environments*.

## Plans

A *plan* specifies, in code, how to deliver a particular application in a particular way.

It lays out the application's supply chain as a graph of interconnected nodes:

* Development tools: source control, CI, build systems, testing systems
* Hosting infrastructure: compute, storage, networking, databases, CDN..
* Software dependencies: operating systems, languages, libraries, frameworks, etc.

The graph models the flow of code and data through the supply chain:
* source code flows from a git repository to a build system;
* system dependencies are combined in a docker image, then uploaded to a registry;
* configuration files are generated then sent to a compute cluster or load balancer;
* etc.

Dagger plans are written in [Cue](https://cuelang.org), a powerful declarative language by the creator of GQL, the language used to deploy all applications at Google.


## Environments

An *environment* is a live implementation of a *plan*, with its own user inputs and state.
The same plan can be executed in multiple environments, for example to differentiate production from staging.

An environment can be updated with `dagger up`. When updating an environment, Dagger determines which inputs have
changed since the last update, and runs them through the corresponding pipelines to produce new outputs.

For example, if an application has a new version of its frontend source code available, but no changes to
the frontend, it will build, test and deploy the new frontend, without changing the backend.

## Relays

*Relays* are the basic components of a *plan*. Each relay is a node in the graph defined by the plan,
performing the task assigned to that node. For example one relay fetches source code; another runs a build;
another uploads a container image; etc.

Relays are standalone software components: they are defined in [Cue](https://cuelang.org), but can
execute code in any language using the [Dagger pipeline API](FIXME).

A relay is made of 3 parts:
* Inputs: data received from the user, or upstream relays
* A processing pipeline: code executed against each new input, using the [pipeline API](FIXME)
* Outputs: data produced by the processing pipeline

Relays run in parallel, with their inputs and outputs interconnected into a special kind of graph,
called a *DAG*. When a relay receives a new input, it runs it through the processing pipeline,
and produces new outputs, which are propagated to downstream relays as inputs, and so on.


## Using third-party relays

Cue includes a complete package system. This makes it easy to create a complex plan in very few
lines of codes, simply by importing relays from third-party packages.

For example, to create a plan involving Github, Heroku and Amazon RDS, one might import the three
corresponding packages:

```
import (
	"dagger.io/github"
	"dagger.io/heroku"
	"dagger.io/amazon/rds"
)

repo: github.#Repository & {
	// Github configuration values
}

backend: heroku.#App & {
	// Heroku configuration values
}

db: rds.#Database & {
	// RDS configuration values
}
```


## Creating a new relay

Sometimes there is no third-party relay available for a particular task in your workflow; or it may exist but need to be customized.

A relay is typically contained in a [cue definition](https://cuetorials.com/overview/foundations/#definitions), with the definition name describing its function.
For example a relay for a git repository might be defined as `#Repository`.

The processing pipeline is a crucial feature of Dagger. It uses the [LLB](https://github.com/moby/buildkit)
executable format pioneered by the Buildkit project. It allows Dagger components to run
sophisticated pipelines to ingest produce artifacts such as source code, binaries, database exports, etc.
Best of all, LLB pipelines can securely build and run any docker container, effectively making Dagger
scriptable in any language.

## Docker compatibility

Thanks to its native support of LLB, Dagger offers native compatibility with Docker.

This makes it very easy to extend an existing Docker-based workflow, including:

* Reusing Dockerfiles and docker-compose files without modification
* Wrapping other deployment tools in a Dagger relay by running them inside a container
* Robust multi-arch and multi-OS support, including Arm and Windows.
* Integration with existing Docker engines and registries
* Integration with Docker for Mac and Docker for Windows on developer machines
