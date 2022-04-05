---
slug: /1205/container-images
displayed_sidebar: europa
---

# Building docker container images

You can use Dagger to build container images, either by executing a Dockerfile, or specifying the build steps natively in CUE. Which method to choose depends on the requirements of your project. You can mix and match builds from both methods in the same plan.

## Executing a Dockerfile

Dagger can natively load and execute Dockerfiles. This is recommended in cases where compatibility with existing Dockerfiles is more important than fully leveraging the power of CUE.

 Here's a simple example of a [Dockerfile](https://docs.docker.com/develop/develop-images/dockerfile_best-practices/) build:

```cue file=../tests/core-concepts/container-images/simple/with-dockerfile.cue
```

## Specifying a build in CUE

You can specify your container build natively in CUE, using the official Docker package: `universe.dagger.io/docker`. This is recommended when you don't need to worry about Dockerfile compatibility, and want to take advantage of the full power of CUE and the Dagger APIs.

Native CUE builds have the same backend as Dockerfile builds, so all the same features are available. Since CUE is a more powerful language than the Dockerfile syntax, every Dockerfile can be ported to an equivalent CUE configuration, but the opposite is not true. The following example produces the same image as above:

```cue file=../tests/core-concepts/container-images/simple/build.cue
```

## Automation

Building images in CUE gives you greater flexibility. For example, you can automate building multiple versions of an image, and deploy, all in Dagger:

```cue file=../tests/core-concepts/container-images/template/dagger.cue
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

```cue file=../tests/core-concepts/container-images/multi-stage/dagger.cue
```
