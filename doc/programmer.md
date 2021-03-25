# Dagger Programmer Guide

## Overview

Dagger works by running *controllers*: specialized programs each automating
the deployment of a particular application in a particular way.

The same application can be deployed in different ways by different controllers, for example to create distinct production and staging environments.

Technically speaking, a controller is a standalone program with its own code and data,
run by the Dagger platform.

Unlike traditional programs which strictly separate code and data,
Dagger merges them into a unified DAG (direct acyclic graph)
using a powerful declarative language called [CUE](https://cuelang.org).
Each node of the DAG represents a step of the controller's deployment plan. 

Unlike traditional programs which run continuously, Dagger controllers are
*reactive*: their DAG is recomputed upon receiving a new input.

The Dagger platform natively supports [LLB](https://github.com/moby/buildkit) pipelines
pioneered by the Buildkit project.
This allows controllers to run sophisticated pipelines to ingest and process
artifacts such as source code, binaries, database exports, ML models, etc.
Best of all, LLB pipelines can securely build and run any docker/OCI container,
effectively allowing Dagger to be scriptable in any language.

## What is a DAG?

A DAG is the basic unit of programming in dagger.
It is a special kind of program which runs as a aipeline of inter-connected computing nodes running in parallel, instead of a sequence of operations to be run by a single node.

DAGs are a powerful way to automate various parts of an application delivery workflow:
build, test, deploy, generate configuration, enforce policies, publish artifacts, etc.

The DAG architecture has many benefits:

  - Because DAGs are made of nodes executing in parallel, they are easy to scale.
  - Because all inputs and outputs are snapshotted and content-addressed, DAGs
  can easily be made repeatable, can be cached aggressively, and can be replayed
  at will.
  - Because nodes are executed by the same container engine as docker-build, DAGs
  can be developed using any language or technology capable of running in a docker.
  Dockerfiles and docker images are natively supported for maximum compatibility.
  - Because DAGs are programmed declaratively with a powerful configuration language,
  they are much easier to test, debug and refactor than traditional programming languages.

To execute a DAG, the dagger runtime JIT-compiles it to a low-level format called llb, and executes it with buildkit. Think of buildkit as a specialized VM for running compute graphs; and dagger as a complete programming environment for that VM.

The tradeoff for all those wonderful features is that a DAG architecture cannot be used for all software: only software than can be run as a pipeline.

## The CUE language

*FIXME*

## The Buildkit runtime environment

*FIXME*
