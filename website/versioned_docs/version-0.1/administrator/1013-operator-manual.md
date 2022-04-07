---
slug: /1013/operator-manual/
---

# Dagger Operator Manual

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

## OpenTracing Support

Both Dagger and buildkit support opentracing. To capture traces to
[Jaeger](https://github.com/jaegertracing/jaeger), set the `JAEGER_TRACE` environment variable to the collection address.

A `docker-compose` file is available to help bootstrap the tracing environment:

```shell
docker-compose -f ./dagger-main/tracing.compose.yaml up -d
export JAEGER_TRACE=localhost:6831
export BUILDKIT_HOST=docker-container://dagger-buildkitd-jaeger

dagger up
```

You can then go to [http://localhost:16686/](http://localhost:16686/) in your browser to see the traces.
