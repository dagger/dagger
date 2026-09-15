---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerTerminalOpts

> **ContainerTerminalOpts** = `object`

## Properties

### cmd?

> `optional` **cmd?**: `string`[]

If set, override the container's default terminal command and invoke these command arguments instead.

***

### experimentalPrivilegedNesting?

> `optional` **experimentalPrivilegedNesting?**: `boolean`

Provides Dagger access to the executed command.

***

### insecureRootCapabilities?

> `optional` **insecureRootCapabilities?**: `boolean`

Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
