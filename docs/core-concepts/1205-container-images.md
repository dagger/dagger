---
slug: /1205/container-images
displayed_sidebar: europa
---

# Building container images

You can use Dagger to build container images. Here's a simple example of a [Dockerfile](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/) build:

```cue file=../tests/core-concepts/container-images/plans/with-dockerfile.cue
```

## Building with CUE

`Dockerfile` files are easy to start, but you can also build images entirely in CUE. The following example produces the same image as above:

```cue file=../tests/core-concepts/container-images/plans/build.cue
```

## Automation

Building images in CUE gives you greater flexibility. For example, you can automate building multiple versions of an image, and deploy, all in Dagger:

```cue file=../tests/core-concepts/container-images/plans/template.cue
```

Now you can deploy all versions:

```shell
dagger do versions
```

Or just build a specific version, without pushing:

```shell
dagger do versions 8.0 build
```

## Multi-stage build

Another common pattern is [multi-stage builds](https://docs.docker.com/develop/develop-images/multistage-build/#use-multi-stage-builds). This allows you to have heavier build images during the build process, and copy the built artifacts into a cleaner and lighter image to run in production.

```cue file=../tests/core-concepts/container-images/plans/multi-stage.cue
```
