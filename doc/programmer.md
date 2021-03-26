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

Each node is a standalone software component, with its own code, inputs and outputs.
The interconnected network of component inputs and outputs forms a special kind of graph called a [DAG]().

Dagger follows a *reactive* programming model: when a component receives a new input
(for example a new version of source code, or a new setting), it recomputes its outputs,
which then propagate to adjacent nodes, and so on. Thus the flow of data through
the DAG mimics the flow of goods through a supply chain.


## Using third-party components

Cue includes a complete package system. This makes it easy to create a complex deployment plan in very few
lines of codes, simply by composing existing packages.

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


## Creating a new component

Sometimes there is no third-party component available for a particular node in the application's supply chain;
or it exists but needs to be customized.

A Dagger component is simply a Cue definition annotated with [LLB](https://github.com/moby/buildkit) pipelines.
LLB is a standard executable format pioneered by the Buildkit project. It allows Dagger components to run
sophisticated pipelines to ingest, and process artifacts such as source code, binaries, database exports, etc.
Best of all LLB pipelines can securely build and run any docker container, effectively making Dagger
scriptable in any language.
