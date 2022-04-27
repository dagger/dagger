---
slug: /1223/custom-buildkit/
---

# Customizing your Buildkit installation

## Using a custom buildkit daemon

Dagger can be configured to use an existing buildkit daemon, running either locally or remotely. This can be done using the environment variable `BUILDKIT_HOST`.

To use a buildkit daemon listening on TCP port `1234` on localhost:

```shell
export BUILDKIT_HOST=tcp://localhost:1234
```

To use a buildkit daemon running in a container named "super-buildkit" on the local docker host:

```shell
export BUILDKIT_HOST=docker-container://super-buildkit
```

## Using a custom remote buildkit running in Docker

Dagger can also be configured to use a remote buildkit daemon running in a Docker container. This an be done using the environment variable `DOCKER_HOST`.

```shell
export DOCKER_HOST=ssh://user@IP
```

You will also need to set the `BUILDKIT_HOST` environment variable explained above.

## Running a custom buildkit container in Docker

To run a customized Buildkit version with Docker, this can be done using the below command:

```shell
docker run -d --name dagger-buildkitd --privileged --network=host docker.io/moby/buildkit:latest
```

## OpenTelemetry Support

Both Dagger and buildkit support opentelemetry. To capture traces to
[Jaeger](https://github.com/jaegertracing/jaeger), set the `OTEL_EXPORTER_JAEGER_ENDPOINT` environment variable to the collection address.

A `docker-compose` file is available to help bootstrap the tracing environment:

```shell
docker-compose -f ./dagger-main/tracing.compose.yaml up -d
export BUILDKIT_HOST=docker-container://dagger-buildkitd-jaeger
export OTEL_EXPORTER_JAEGER_ENDPOINT=http://localhost:14268/api/traces
export JAEGER_TRACE=localhost:6831

dagger up
```

You can then go to [http://localhost:16686/](http://localhost:16686/) in your browser to see the traces.
