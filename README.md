## Introduction to Dagger 

[![Watch the video](https://user-images.githubusercontent.com/101831275/220751200-add7a2be-884f-4d59-b24d-62b22ffb3a00.png)](https://youtu.be/HHhttaiHtm4)

## What is Dagger?

Dagger is a programmable CI/CD engine that runs your pipelines in containers.

### Programmable

Develop your CI/CD pipelines as code, in the same programming language as your application.

### Runs your pipelines in containers

Dagger executes your pipelines entirely as [standard OCI containers](https://opencontainers.org/). This has several benefits:

* **Instant local testing**
* **Portability**: the same pipeline can run on your local machine, a CI runner, a dedicated server, or any container hosting service.
* **Superior caching**: every operation is cached by default, and caching works the same everywhere
* **Compatibility** with the Docker ecosystem: if it runs in a container, you can add it to your pipeline.
* **Cross-language instrumentation**: teams can use each other's tools without learning each other's language.

## Who is it for?

Dagger may be a good fit if you are...

* A developer wishing your CI pipelines were code instead of YAML
* Your team's "designated devops person", hoping to replace a pile of artisanal scripts with something more powerful
* A platform engineer writing custom tooling, with the goal of unifying continuous delivery across organizational silos
* A cloud-native developer advocate or solutions engineer, looking to demonstrate a complex integration on short notice

## Learn more

* [How does it work?](https://docs.dagger.io/#how-does-it-work)
* [Getting started](https://docs.dagger.io/#getting-started)
* [Examples](https://github.com/dagger/examples)
* [Join the Dagger community server](https://discord.gg/ufnyBtc8uY)
* [Follow us on Twitter](https://twitter.com/dagger_io)
* Join a [Dagger community call](https://dagger.io/events).
