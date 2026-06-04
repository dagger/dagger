[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / DirectoryDockerBuildOpts

# Type Alias: DirectoryDockerBuildOpts

> **DirectoryDockerBuildOpts** = `object`

## Properties

### buildArgs?

> `optional` **buildArgs?**: [`BuildArg`](BuildArg.md)[]

Build arguments to use in the build.

***

### dockerfile?

> `optional` **dockerfile?**: `string`

Path to the Dockerfile to use (e.g., "frontend.Dockerfile").

***

### noInit?

> `optional` **noInit?**: `boolean`

If set, skip the automatic init process injected into containers created by RUN statements.

This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.

***

### platform?

> `optional` **platform?**: [`Platform`](Platform.md)

The platform to build.

***

### secrets?

> `optional` **secrets?**: [`Secret`](../classes/Secret.md)[]

Secrets to pass to the build.

They will be mounted at /run/secrets/[secret-name].

***

### ssh?

> `optional` **ssh?**: [`Socket`](../classes/Socket.md)

A socket to use for SSH authentication during the build

(e.g., for Dockerfile RUN --mount=type=ssh instructions).

Typically obtained via host.unixSocket() pointing to the SSH_AUTH_SOCK.

***

### target?

> `optional` **target?**: `string`

Target build stage to build.
