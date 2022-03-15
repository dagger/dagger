---
slug: /1216/ci-cd-for-go-project
displayed_sidebar: europa
---

# Go on Docker Hub

Dagger stand as a powerful CI/CD tool that works on any environment.

For instance, you can use [go package](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/go)
to control the whole CI/CD process, from test to push into a remote registry.

:::tip
Following examples can be used as a template for any standalone go project.
:::

## Test

```cue file=../tests/use-cases/ci-cd-for-go-project/test/dagger.cue
```

You can then run unit test in your go project with

```shell
dagger do unit-test
```

<!-- FIXME: we should write a bunch of documentation about TDD with dagger -->
:::tip
You can also use dagger to write integration tests
:::

## Build

```cue file=../tests/use-cases/ci-cd-for-go-project/build/dagger.cue
```

:::tip
You can control the binary platform with `os` and `arch` field.
:::

You can then build your binary with

```shell
dagger do build
```

## Push

```cue file=../tests/use-cases/ci-cd-for-go-project/push/dagger.cue
```

You can then build and push your go project with

```shell
dagger do push
```

## Push multi-platform

Coming soon
