# Dagger: standardize your dev environments

## What is Dagger?

Dagger defines a new standard for packaging, distributing and running software environments as a containerized DAG. Once "daggerized", environments have the following properties:

- **Portable**: any machine that can run standard containers can run a DAG.
- **Familiar**: keep using the tools you know, packaged in containers.
- **Scriptable**: automate custom tasks in any language, using a powerful API and growing list of SDKs
- **Extensible**: easily incorporate the capabilities of other environments into your own
- **Scalable**: all operations in the DAG are scheduled concurrently, which allows seamless parallelization across cores and if needed, across machines in a cluster (*coming soon*)
- **Fast**: all operations in the DAG are continuously cached, making all computation magically incremental. As a result, daggerizing an environment typically makes builds and other cpu-intensive tasks much faster out of the box.

## What kind of environments?

In theory, any software environment can be daggerized, as long as its individual components can run in a container. In practice, Dagger is most commonly used for non-production environments, such as:

* Development
* Build
* Test, especiall end-to-end integration testing
* CI/CD
* QA and staging, also known as "preview environments"
* MLOps
* Data engineering

Although Daggerized environments often integrate into production - for example by building production artifacts, or deploying to a remote production environment - the production environment itself rarely runs in Dagger.


## What is a containerized DAG?

A containerized DAG is a computing environment which can run programs made of many concurrent containers, each running a simple operation, with data flowing between the containers in a graph layout.

It inherits the properties of containers (portable, shareable, repeatable) with additional properties (parallelizeable, cached by default, extensible, composable, scriptable, dynamic, embeddable) you can think of the CDAG standard as an extension of the OCI standard.

## Why Dagger?

Daggerizing your team's environments can solve or mitigate the following problems:

- New developers take too long to be productive
- Tests pass in development but not in CI
- CI environment lags behind development
- End-to-end testing requires artisanal scripts to glue incompatible environments together
- Platform teams struggle to integrate MLops environments into the continuous delivery pipeline
- Major stack changes cause disruption or delays, because environments are hard to change and test reliably
- CI/CD pipelines gets slower as the stack and team grow

## Why not just use a container?

There is an emerging trend of running development environments in a container (see Devbox, Devpod, Gitpod, Nix, Replit). This is a considerable improvement from the previous status quo. But it's not enough, because most environments are too complex and dynamic to fit in one container. Docker-compose is slightly better, because it can package a multi-container application; but in spite of its name, Docker-compose is neither composable nor scriptable.

## Learn more

* [Join the Dagger community server](https://discord.gg/ufnyBtc8uY)
* [Follow us on Twitter](https://twitter.com/dagger_io)
* Join a [Dagger community call](https://dagger.io/events).
