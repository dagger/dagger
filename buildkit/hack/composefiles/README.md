# Buildkit Docker Compose

The `hack/compose` script provides a convenient development environment for building and testing buildkit with some
useful supporting services and configuration using docker compose to assemble the environment.
This configuration is not meant for production deployments.

## Usage

The primary way to use the script is as a substitute to `docker compose`.

```bash
$ hack/compose up -d
```

All arguments to this script will be forwarded to `docker compose`.
This will use the `moby/buildkit:local` image to create a buildkit container.
The image can be either built with `make images` or the `--build` flag can be used.

```bash
$ hack/compose up -d --build
```

To access the newly created development buildkit, use either:

```bash
$ buildctl --addr docker-container://buildkit-dev ...
```

or alternatively configure a new builder with buildx.

```bash
$ docker buildx create \
    --bootstrap \
    --name dev \
    --driver remote \
    docker-container://buildkit-dev
```

## Extending Configuration

The compose definition can be extended by using `-f $COMPOSE_FILE` to extend the configuration.
These files are in the `extensions` folder.

As an example, an extension can be used for aiding with buildx metrics development:

```bash
$ hack/compose -f hack/composefiles/extensions/buildx.yaml up -d --build
```

This will modify the `otel-collector` configuration to emit buildx metrics to the debug exporter.

Some of the files in this directory can be combined and some conflict with each other. In particular,
configurations that modify the `otel-collector` probably conflict with each other.

## Running the debugger

The debugger is exposed for the local buildkit when `--build` is used to build the local image.
This can be accessed using delve or any GUI client that uses delve (such as JetBrains GoLand).

```bash
$ dlv connect localhost:5000
```

More detailed documentation on remote debugging is [here](https://github.com/moby/buildkit/blob/master/docs/dev/remote-debugging.md#connecting-to-the-port-command-line).

## OpenTelemetry

OpenTelemetry is automatically configured for traces.
These can be visualized through Jaeger.

### Traces

Traces are sent to [Jaeger](http://localhost:16686).
Traces that happen in `buildctl` and `buildx` are automatically sent to `buildkit` which forwards them to Jaeger.
