# Dagger

*WARNING: Dagger is alpha-quality software. It has many bugs, the user interface is minimal, and it may change in incompatible ways at any time. If you are still willing to try it, thank you! We appreciate your help and encourage you to ask questions and report issues.*

Dagger is an *Application Delivery as Code* (ADC) platform.

Using Dagger, software teams can replace their artisanal deployment scripts with a state-of-the-art deployment pipeline, tailor-made for their application and infrastructure, in just a few lines of code.

- Do you spend too much time getting your home-made deployment scripts to work?
- Do you wish you could just use a PaaS like Heroku or Firebase but can’t because your stack is too custom?
- Do you put off infrastructure and workflow improvements because it would slow down development?
- Do you wish you could visualize your entire application delivery workflow as code, all in one place?

If you answered “yes” to any of the above, Dagger might be a good fit for you.

## The problem with PaaS

A PaaS, or platform-as-a-service, is the glue between an application and the cloud infrastructure running it. It automates the deployment process and provides a simplified view of the underlying infrastructure, which makes developers more productive.

However, despite the undeniable productivity benefits of using a PaaS, most applications today do not. Why? Because it's not flexible enough: each PaaS only supports certain types of application stacks and infrastructure. Applications that cannot adapt to the platform are simply not supported, and instead are deployed by a mosaic of specialized tools, usually glued together in an artisanal shell script or equivalent.

But what if we didn’t have to choose between productivity and flexibility? What if we could have both? That’s where Dagger comes in. With Dagger, *each application defines its own PaaS*, perfectly tailored to its existing stack and infrastructure. The platform adapts to the application, instead of the other way around.

And because it’s defined *as code*, this custom PaaS can easily be changed over time, as the application stack and infrastructure evolves.


## Getting started


### Installing the dagger command-line

1. Build the `dagger` command-line tool. You will need [Go](https://golang.org) version 1.16 or later.

```
$ make
```

2. Copy the `dagger` tool to a location listed in your `$PATH`. For example, to copy it to `/usr/local/bin`:

```
$ cp ./cmd/dagger/dagger /usr/local/bin
```

3. Compute a test configuration

Currently `dagger` can only do one thing: compute a configuration with optional inputs, and print the result.

If you are confused by how to use this for your application, that is normal: `dagger compute` is a low-level command
which exposes the naked plumbing of the Dagger engine. In future versions, more user-friendly commands will be available
which hide the complexity of `dagger compute` (but it will always be available to power users, of course!).

Here is an example command, using an example configuration:

```
$ dagger compute ./examples/simple --input-string www.host=mysuperapp.com --input-dir www.source=.
```

### Custom buildkit setup

Dagger can be configured to use an existing buildkit daemon, running either locally or remotely. This can be done using two environment variables: `BUILDKIT_HOST` and `DOCKER_HOST`.

To use a buildkit daemon listening on TCP port `1234` on localhost:

```
$ export BUILDKIT_HOST=tcp://localhost:1234
```

To use a buildkit daemon running in a container named "super-buildkit" on the local docker host:

```
$ export BUILDKIT_HOST=docker-container://super-buildkit
```

To use a buildkit daemon running on a remote docker host (be careful to properly secure remotely accessible docker hosts!)

```
$ export BUILDKIT_HOST=docker-container://super-buildkit
$ export DOCKER_HOST=tcp://my-remote-docker-host:2376
```

### OpenTracing Support

Both Dagger and buildkit support opentracing. To capture traces to
[Jaeger](https://github.com/jaegertracing/jaeger), ), set the `JAEGER_TRACE` environment variable to the collection address.

A `docker-compose` file is available to help bootstrap the tracing environment:

```sh
docker-compose -f ./tracing.compose.yaml up -d
export JAEGER_TRACE=localhost:6831
export BUILDKIT_HOST=docker-container://dagger-buildkitd-jaeger

dagger compute ...
```

You can then go to [http://localhost:16686/](http://localhost:16686/) in your browser to see the traces.


## The Dagger programming model

*FIXME*.

## Dagger and your application

*FIXME*.

## Dagger and your infrastructure

*FIXME*.
