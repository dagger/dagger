[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerWithExecOpts

# Type Alias: ContainerWithExecOpts

> **ContainerWithExecOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### expect?

> `optional` **expect?**: [`ReturnType`](../enumerations/ReturnType.md)

Exit codes this command is allowed to exit with without error

***

### experimentalPrivilegedNesting?

> `optional` **experimentalPrivilegedNesting?**: `boolean`

Provides Dagger access to the executed command.

***

### insecureRootCapabilities?

> `optional` **insecureRootCapabilities?**: `boolean`

Execute the command with all root capabilities. Like --privileged in Docker

DANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.

***

### noInit?

> `optional` **noInit?**: `boolean`

Skip the automatic init process injected into containers by default.

Only use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.

***

### redirectStderr?

> `optional` **redirectStderr?**: `string`

Redirect the command's standard error to a file in the container. Example: "./stderr.txt"

***

### redirectStdin?

> `optional` **redirectStdin?**: `string`

Redirect the command's standard input from a file in the container. Example: "./stdin.txt"

***

### redirectStdout?

> `optional` **redirectStdout?**: `string`

Redirect the command's standard output to a file in the container. Example: "./stdout.txt"

***

### stdin?

> `optional` **stdin?**: `string`

Content to write to the command's standard input. Example: "Hello world")

***

### useEntrypoint?

> `optional` **useEntrypoint?**: `boolean`

Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.
