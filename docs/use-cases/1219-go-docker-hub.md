---
slug: /1219/go-docker-hub
displayed_sidebar: europa
---

# Go on Docker Hub

Dagger stand as a powerful CI/CD tool that works on any environment.

For instance, you can use [go package](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/go)
to control the whole CI/CD process, from test to push into a remote registry.

:::tip
Following examples can be used as a template for any standalone go project.
:::

## Retrieve Go project

First important step is to make go project accessible in dagger plan.

You can indeed choose which files to include in the filesystem.  
Since it's a Golang project, filesystem should contain module and every go
source files:

```cue file=../tests/use-cases/ci-cd-for-go-project/retrieve-go-project/dagger.cue
```

:::tip
To make it more accessible in actions, you can set a private field that will
act as an alias.
:::

## Build a Go base image

[Dagger go universe](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/go)
provide a [base image](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/image.cue)
to build your pipeline but your project may use `CGO` or any external dependencies.

You can customize that base image to install required dependencies:

```cue file=../tests/use-cases/ci-cd-for-go-project/base.cue.fragment
```

## Run unit test

Before deliver your application, you certainly want to run unit test.

By using previous steps, you can use the [test](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/test.cue)
definition to run your unit test:

```cue file=../tests/use-cases/ci-cd-for-go-project/test.cue.fragment
```

<!-- FIXME(TomChv): we should write a bunch of documentation about TDD with dagger -->
:::tip
You can also use dagger to write integration tests
:::

## Build Go binary

To put your go project on docker hub, you first need to compile a binary.

Go universe expose a [build](https://github.com/dagger/dagger/blob/main/pkg/universe.dagger.io/go/build.cue)
definition so you can build a binary:

```cue file=../tests/use-cases/ci-cd-for-go-project/build.cue.fragment
```

:::tip
You can control the binary platform with `os` and `arch` field.
:::

## Prepare docker image

To make it usable by other user, you must put your binary in an image and set an entrypoint.

For optimisation purpose, you can use alpine as base image to contain your binary:

```cue file=../tests/use-cases/ci-cd-for-go-project/image.cue.fragment
```

## Push to Docker Hub

To push an image to docker hub, you will need to forward credential to allow
dagger push.

To not hard code your docker password in the plan, you can retrieve it as an
environment value:

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

After merging all examples, you will have a complete CI/CD to deliver a go
binary on Docker Hub.

```cue file=../tests/use-cases/ci-cd-for-go-project/complete-ci-cd/dagger.cue
```

You can then use `dagger do` to select which action you want to run.

## Push multi-platform

Coming soon...
