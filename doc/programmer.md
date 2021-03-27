# Dagger Programmer Guide

## Overview

A Dagger deployment is a continuously running workflow delivering a specific application in a specific way.

The same application can be delivered via different deployments, each with a different configuration.
For example a production deployment might include manual validation and addition performance testing,
while a staging deployment might automatically deploy from a git branch, load test data into the database,
and run on a separate cluster.

A deployment is made of 3 parts: a deployment plan, inputs, and outputs.


## The Deployment Plan

The deployment plan is the source code of the deployment. It is written in [Cue](https://cuelang.org),
a powerful declarative language by the creator of GQL, the language used to deploy all applications at Google.

The deployment plan lays out every node in the application supply chain, and how they are interconnected:

* Development tools: source control, CI, build systems, testing systems
* Hosting infrastructure: compute, storage, networking, databases, CDN..
* Software dependencies: operating systems, languages, libraries, frameworks, etc.

Nodes are interconnected to model the flow of code and data through the supply chain:
source code flows from a git repository to a build system; system dependencies are
combined in a docker image, then uploaded to a registry; configuration files are
generated then sent to a compute cluster or load balancer; etc.

## Relays

Dagger executes deployments by running *relays*.

A relay is a standalone software component assigned to a single node in the deployment plan.
One relay fetches might source code; another runs the build system; another uploads the container image; etc.

Relays are written in Cue, like the deployment plan they are part of. A relay is made of 3 parts:
* Inputs: data received from the user, or upstream relays
* A processing pipeline: code executed against each new input
* Outputs: data produced by the processing pipeline

Relays run in parallel, with their inputs and outputs interconnected into a special kind of graph,
called a *DAG*. When a relay receives a new input, it runs it through the processing pipeline,
and produces new outputs, which are propagated to downstream relays as inputs, and so on.


## Using third-party relays

Cue includes a complete package system. This makes it easy to create a complex deployment plan in very few
lines of codes, simply by importing relays from third-party packages.

For example, to create a deployment plan involving Github, Heroku and Amazon RDS, one might import the three
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

Sometimes there is no third-party relay available for a particular node in the deployment plan;
or it may exist but need to be customized.

A relay is typically contained in a cue definition, with the definition name reflecting its function.
For example a relay for a git repository might be defined as `#Repository`.

The inputs and outputs of a relay are simply cue values in the definition.

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
