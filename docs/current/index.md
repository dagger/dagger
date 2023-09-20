---
slug: /
displayed_sidebar: "current"
---

# Dagger Documentation

## What is Dagger?

Dagger is an integrated platform to orchestrate the delivery of applications to the cloud from start to finish. The Dagger Platform includes the Dagger Engine, Dagger Cloud, and the Dagger SDKs.

## What is the Dagger Engine?

The Dagger Engine is a programmable CI/CD engine that runs your pipelines in containers.

### Programmable

Develop your CI/CD pipelines as code, in the same programming language as your application.

### Runs your pipelines in containers

The Dagger Engine executes your pipelines entirely as [standard OCI containers](https://opencontainers.org/). This has several benefits:

* **Instant local testing**
* **Portability**: the same pipeline can run on your local machine, a CI runner, a dedicated server, or any container hosting service.
* **Superior caching**: every operation is cached by default, and caching works the same everywhere
* **Compatibility** with the Docker ecosystem: if it runs in a container, you can add it to your pipeline.
* **Cross-language instrumentation**: teams can use each other's tools without learning each other's language.

## What is Dagger Cloud?

Dagger Cloud complements the Dagger Engine with a production-grade control plane. Features of Dagger Cloud include pipeline visualization, operational insights, and distributed caching.

## Who is it for?

Dagger may be a good fit if you are...

* A developer wishing your CI pipelines were code instead of YAML.
* Your team's "designated devops person", hoping to replace a pile of artisanal scripts with something more powerful.
* A platform engineer writing custom tooling, with the goal of unifying continuous delivery across organizational silos.
* A cloud-native developer advocate or solutions engineer, looking to demonstrate a complex integration on short notice.

## How does it work?

{@include: ./partials/_how_does_dagger_work.md}

## Getting started

To get started with the Dagger Engine, use our [Dagger Engine quickstart](./quickstart/index.mdx), which walks you through the basics of creating and using a pipeline with the Dagger SDKs. Alternatively, choose an SDK and follow that SDK's getting started guide.

To use Dagger in production, learn about [Dagger Cloud](https://dagger.io/cloud) and use our [Dagger Cloud guide](./cloud/572923-get-started.md) to connect Dagger with your CI provider or CI tool.

## Which SDK should I use?

| If you are... | then you should... |
| -- | -- |
| a Go developer | Use the [Go SDK](sdk/go) |
| a Python developer | Use the [Python SDK](sdk/python) |
| a TypeScript/JavaScript developer | Use the [Node.js SDK](sdk/nodejs) |
| looking for an excuse to learn Go | Use the [Go SDK](sdk/go) |
| looking for an excuse to learn Python | Use the [Python SDK](sdk/python) |
| looking for an excuse to learn TypeScript/JavaScript | Use the [Node.js SDK](sdk/nodejs) |
| waiting for your favorite language to be supported | [Let us know which one](https://airtable.com/shrzABOn1wCk5yBF4), and we'll notify you when it is ready |
| a GraphQL veteran | Use the [GraphQL API](api) |
| a fan of shell scripts | Use the [CLI](cli) |
| Not sure which SDK to choose 🤷 | In doubt, try the [Go SDK](sdk/go). It does not require advanced Go knowledge, and what you learn will transpose well to future SDKs
