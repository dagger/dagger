[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / DirectoryTerminalOpts

# Type Alias: DirectoryTerminalOpts

> **DirectoryTerminalOpts** = `object`

## Properties

### cmd?

> `optional` **cmd?**: `string`[]

If set, override the container's default terminal command and invoke these command arguments instead.

***

### container?

> `optional` **container?**: [`Container`](../classes/Container.md)

If set, override the default container used for the terminal.

***

### experimentalPrivilegedNesting?

> `optional` **experimentalPrivilegedNesting?**: `boolean`

Provides Dagger access to the executed command.

***

### insecureRootCapabilities?

> `optional` **insecureRootCapabilities?**: `boolean`

Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
