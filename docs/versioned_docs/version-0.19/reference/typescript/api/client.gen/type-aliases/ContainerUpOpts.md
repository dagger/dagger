[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerUpOpts

# Type Alias: ContainerUpOpts

> **ContainerUpOpts** = `object`

## Properties

### args?

> `optional` **args?**: `string`[]

Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).

If empty, the container's default command is used.

***

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### experimentalPrivilegedNesting?

> `optional` **experimentalPrivilegedNesting?**: `boolean`

Provides Dagger access to the executed command.

***

### insecureRootCapabilities?

> `optional` **insecureRootCapabilities?**: `boolean`

Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.

***

### noInit?

> `optional` **noInit?**: `boolean`

If set, skip the automatic init process injected into containers by default.

This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.

***

### ports?

> `optional` **ports?**: [`PortForward`](PortForward.md)[]

List of frontend/backend port mappings to forward.

Frontend is the port accepting traffic on the host, backend is the service port.

***

### random?

> `optional` **random?**: `boolean`

Bind each tunnel port to a random port on the host.

***

### useEntrypoint?

> `optional` **useEntrypoint?**: `boolean`

If the container has an entrypoint, prepend it to the args.
