---
slug: /1013/operator-manual/
displayed_sidebar: '0.1'
---

<!--
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!!! OLD DOCS. NOT MAINTAINED. !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
-->

import CautionBanner from '../\_caution-banner.md'

# Dagger Operator Manual

<CautionBanner old="0.1" new="0.2" />

## Custom buildkit setup

Dagger can be configured to use an existing buildkit daemon, running either locally or remotely. This can be done using two environment variables: `BUILDKIT_HOST` and `DOCKER_HOST`.

To use a buildkit daemon listening on TCP port `1234` on localhost:

```shell
export BUILDKIT_HOST=tcp://localhost:1234
```

To use a buildkit daemon running in a container named "super-buildkit" on the local docker host:

```shell
export BUILDKIT_HOST=docker-container://super-buildkit
```

To use a buildkit daemon running on a remote docker host (be careful to properly secure remotely accessible docker hosts!)

```shell
export BUILDKIT_HOST=docker-container://super-buildkit
export DOCKER_HOST=tcp://my-remote-docker-host:2376
```

## Custom runtime setup

If you aren't using the Docker container runtime in your environment, you simply have to run buildkit daemon in the runtime of your choice and instruct Dagger to use it.

To create a buildkit daemon in Podman:

```shell
podman run -d --name buildkitd --privileged moby/buildkit:latest
```

To the use that daemon, set the `BUILDKIT_HOST` environment variable with the correct scheme. Continuing with the Podman example:

```shell
export BUILDKIT_HOST=podman-container://buildkitd
```

Dagger currently supports these connection schemes:

- `docker-container://`
- `podman-container://`
- `kube-pod://`

## OpenTelemetry Support

Both Dagger and buildkit support opentelemetry. To capture traces to
[Jaeger](https://github.com/jaegertracing/jaeger), set the `OTEL_EXPORTER_JAEGER_ENDPOINT` environment variable to the collection address.

A `docker-compose` file is available to help bootstrap the tracing environment:

```shell
docker-compose -f ./dagger-main/tracing.compose.yaml up -d
export BUILDKIT_HOST=docker-container://dagger-buildkitd-jaeger
export OTEL_EXPORTER_JAEGER_ENDPOINT=http://localhost:14268/api/traces

dagger up
```

You can then go to [http://localhost:16686/](http://localhost:16686/) in your browser to see the traces.
