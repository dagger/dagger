---
slug: /1219/go-docker-hub
displayed_sidebar: '0.2'
---

# Go on Docker Hub

Dagger stands as a powerful CI/CD tool that works on any environment.

For instance, you can use the [Dagger Go package](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/go)
to control the whole CI/CD process, from testing to pushing into a remote registry.

:::tip
The following examples can be used as a template for any standalone Go project.
:::

## Retrieve Go project

The first step is to make your Go project accessible to the Dagger plan.

You can indeed choose which files to include. Since it's a Golang project
it should contain the module and all Go source files:

```cue file=../tests/use-cases/ci-cd-for-go-project/retrieve-go-project/dagger.cue

```

:::tip
To make it more accessible in actions, you can set a private field that will
act as an alias.
:::

## Build a Go base image

The [universe.dagger.io/go](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/go)
package provides a [base image](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/image.cue)
to build your pipeline, but your project may use `CGO` or any external dependencies.

You can customize the base image to install required dependencies:

```cue file=../tests/use-cases/ci-cd-for-go-project/base.cue.fragment

```

## Run unit tests

Before delivering your application, you certainly want to run unit tests.

Use the [#Test](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/test.cue)
definition:

```cue file=../tests/use-cases/ci-cd-for-go-project/test.cue.fragment

```

<!-- FIXME(TomChv): we should write a bunch of documentation about TDD with dagger -->

:::tip
You can also use Dagger to write integration tests.
:::

## Build Go binary

To put your Go project on Docker Hub, you first need to compile a binary.

Use the [#Build](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/build.cue)
definition to do that:

```cue file=../tests/use-cases/ci-cd-for-go-project/build.cue.fragment

```

:::tip
You can control the binary platform with `os` and `arch` fields.
:::

## Prepare docker image

To make it usable for other users, you must put your binary in an image and set an entrypoint.

For optimization purposes, you can use alpine as the base image to contain your binary:

```cue file=../tests/use-cases/ci-cd-for-go-project/image.cue.fragment

```

## Push to Docker Hub

To push an image to Docker Hub, you will need your private credentials.

To not hard code your docker password in the plan, you can retrieve it
from your environment:

```cue
dagger.#Plan & {
    client: {
        // ...

        env: DOCKER_PASSWORD: dagger.#Secret
    }
}
```

You can now push your image:

```cue file=../tests/use-cases/ci-cd-for-go-project/push.cue.fragment

```

## Complete CI/CD

After merging all examples, you will have a complete CI/CD to deliver a Go
binary on Docker Hub.

```cue file=../tests/use-cases/ci-cd-for-go-project/complete-ci-cd/dagger.cue

```

You can then use `dagger do` to select which action you want to run.

## Push multi-platform

Coming soon...
