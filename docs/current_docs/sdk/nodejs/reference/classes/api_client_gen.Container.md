---
id: "api_client_gen.Container"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Container

An OCI-compatible container, also known as a docker container.

## Hierarchy

- `BaseClient`

  ↳ **`Container`**

## Constructors

### constructor

**new Container**(`parent?`, `_id?`, `_envVariable?`, `_export?`, `_imageRef?`, `_label?`, `_platform?`, `_publish?`, `_shellEndpoint?`, `_stderr?`, `_stdout?`, `_sync?`, `_user?`, `_workdir?`): [`Container`](api_client_gen.Container.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ContainerID`](../modules/api_client_gen.md#containerid) |
| `_envVariable?` | `string` |
| `_export?` | `boolean` |
| `_imageRef?` | `string` |
| `_label?` | `string` |
| `_platform?` | [`Platform`](../modules/api_client_gen.md#platform) |
| `_publish?` | `string` |
| `_shellEndpoint?` | `string` |
| `_stderr?` | `string` |
| `_stdout?` | `string` |
| `_sync?` | [`ContainerID`](../modules/api_client_gen.md#containerid) |
| `_user?` | `string` |
| `_workdir?` | `string` |

#### Returns

[`Container`](api_client_gen.Container.md)

#### Overrides

BaseClient.constructor

## Properties

### \_envVariable

 `Private` `Optional` `Readonly` **\_envVariable**: `string` = `undefined`

___

### \_export

 `Private` `Optional` `Readonly` **\_export**: `boolean` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ContainerID`](../modules/api_client_gen.md#containerid) = `undefined`

___

### \_imageRef

 `Private` `Optional` `Readonly` **\_imageRef**: `string` = `undefined`

___

### \_label

 `Private` `Optional` `Readonly` **\_label**: `string` = `undefined`

___

### \_platform

 `Private` `Optional` `Readonly` **\_platform**: [`Platform`](../modules/api_client_gen.md#platform) = `undefined`

___

### \_publish

 `Private` `Optional` `Readonly` **\_publish**: `string` = `undefined`

___

### \_shellEndpoint

 `Private` `Optional` `Readonly` **\_shellEndpoint**: `string` = `undefined`

___

### \_stderr

 `Private` `Optional` `Readonly` **\_stderr**: `string` = `undefined`

___

### \_stdout

 `Private` `Optional` `Readonly` **\_stdout**: `string` = `undefined`

___

### \_sync

 `Private` `Optional` `Readonly` **\_sync**: [`ContainerID`](../modules/api_client_gen.md#containerid) = `undefined`

___

### \_user

 `Private` `Optional` `Readonly` **\_user**: `string` = `undefined`

___

### \_workdir

 `Private` `Optional` `Readonly` **\_workdir**: `string` = `undefined`

## Methods

### asService

**asService**(): [`Service`](api_client_gen.Service.md)

Turn the container into a Service.

Be sure to set any exposed ports before this conversion.

#### Returns

[`Service`](api_client_gen.Service.md)

___

### asTarball

**asTarball**(`opts?`): [`File`](api_client_gen.File.md)

Returns a File representing the container serialized to a tarball.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ContainerAsTarballOpts`](../modules/api_client_gen.md#containerastarballopts) |

#### Returns

[`File`](api_client_gen.File.md)

___

### build

**build**(`context`, `opts?`): [`Container`](api_client_gen.Container.md)

Initializes this container from a Dockerfile build.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `context` | [`Directory`](api_client_gen.Directory.md) | Directory context used by the Dockerfile. |
| `opts?` | [`ContainerBuildOpts`](../modules/api_client_gen.md#containerbuildopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### defaultArgs

**defaultArgs**(): `Promise`\<`string`[]\>

Retrieves default arguments for future commands.

#### Returns

`Promise`\<`string`[]\>

___

### directory

**directory**(`path`): [`Directory`](api_client_gen.Directory.md)

Retrieves a directory at the given path.

Mounts are included.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | The path of the directory to retrieve (e.g., "./src"). |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### entrypoint

**entrypoint**(): `Promise`\<`string`[]\>

Retrieves entrypoint to be prepended to the arguments of all commands.

#### Returns

`Promise`\<`string`[]\>

___

### envVariable

**envVariable**(`name`): `Promise`\<`string`\>

Retrieves the value of the specified environment variable.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the environment variable to retrieve (e.g., "PATH"). |

#### Returns

`Promise`\<`string`\>

___

### envVariables

**envVariables**(): `Promise`\<[`EnvVariable`](api_client_gen.EnvVariable.md)[]\>

Retrieves the list of environment variables passed to commands.

#### Returns

`Promise`\<[`EnvVariable`](api_client_gen.EnvVariable.md)[]\>

___

### experimentalWithAllGPUs

**experimentalWithAllGPUs**(): [`Container`](api_client_gen.Container.md)

EXPERIMENTAL API! Subject to change/removal at any time.

experimentalWithAllGPUs configures all available GPUs on the host to be accessible to this container.
This currently works for Nvidia devices only.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### experimentalWithGPU

**experimentalWithGPU**(`devices`): [`Container`](api_client_gen.Container.md)

EXPERIMENTAL API! Subject to change/removal at any time.

experimentalWithGPU configures the provided list of devices to be accesible to this container.
This currently works for Nvidia devices only.

#### Parameters

| Name | Type |
| :------ | :------ |
| `devices` | `string`[] |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### export

**export**(`path`, `opts?`): `Promise`\<`boolean`\>

Writes the container as an OCI tarball to the destination file path on the host for the specified platform variants.

Return true on success.
It can also publishes platform variants.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Host's destination path (e.g., "./tarball"). Path can be relative to the engine's workdir or absolute. |
| `opts?` | [`ContainerExportOpts`](../modules/api_client_gen.md#containerexportopts) | - |

#### Returns

`Promise`\<`boolean`\>

___

### exposedPorts

**exposedPorts**(): `Promise`\<[`Port`](api_client_gen.Port.md)[]\>

Retrieves the list of exposed ports.

This includes ports already exposed by the image, even if not
explicitly added with dagger.

#### Returns

`Promise`\<[`Port`](api_client_gen.Port.md)[]\>

___

### file

**file**(`path`): [`File`](api_client_gen.File.md)

Retrieves a file at the given path.

Mounts are included.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | The path of the file to retrieve (e.g., "./README.md"). |

#### Returns

[`File`](api_client_gen.File.md)

___

### from

**from**(`address`): [`Container`](api_client_gen.Container.md)

Initializes this container from a pulled base image.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `address` | `string` | Image's address from its registry. Formatted as [host]/[user]/[repo]:[tag] (e.g., "docker.io/dagger/dagger:main"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### id

**id**(): `Promise`\<[`ContainerID`](../modules/api_client_gen.md#containerid)\>

A unique identifier for this container.

#### Returns

`Promise`\<[`ContainerID`](../modules/api_client_gen.md#containerid)\>

___

### imageRef

**imageRef**(): `Promise`\<`string`\>

The unique image reference which can only be retrieved immediately after the 'Container.From' call.

#### Returns

`Promise`\<`string`\>

___

### import\_

**import_**(`source`, `opts?`): [`Container`](api_client_gen.Container.md)

Reads the container from an OCI tarball.

NOTE: this involves unpacking the tarball to an OCI store on the host at
$XDG_CACHE_DIR/dagger/oci. This directory can be removed whenever you like.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `source` | [`File`](api_client_gen.File.md) | File to read the container from. |
| `opts?` | [`ContainerImportOpts`](../modules/api_client_gen.md#containerimportopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### label

**label**(`name`): `Promise`\<`string`\>

Retrieves the value of the specified label.

#### Parameters

| Name | Type |
| :------ | :------ |
| `name` | `string` |

#### Returns

`Promise`\<`string`\>

___

### labels

**labels**(): `Promise`\<[`Label`](api_client_gen.Label.md)[]\>

Retrieves the list of labels passed to container.

#### Returns

`Promise`\<[`Label`](api_client_gen.Label.md)[]\>

___

### mounts

**mounts**(): `Promise`\<`string`[]\>

Retrieves the list of paths where a directory is mounted.

#### Returns

`Promise`\<`string`[]\>

___

### pipeline

**pipeline**(`name`, `opts?`): [`Container`](api_client_gen.Container.md)

Creates a named sub-pipeline

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Pipeline name. |
| `opts?` | [`ContainerPipelineOpts`](../modules/api_client_gen.md#containerpipelineopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### platform

**platform**(): `Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

The platform this container executes and publishes as.

#### Returns

`Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

___

### publish

**publish**(`address`, `opts?`): `Promise`\<`string`\>

Publishes this container as a new image to the specified address.

Publish returns a fully qualified ref.
It can also publish platform variants.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `address` | `string` | Registry's address to publish the image to. Formatted as [host]/[user]/[repo]:[tag] (e.g. "docker.io/dagger/dagger:main"). |
| `opts?` | [`ContainerPublishOpts`](../modules/api_client_gen.md#containerpublishopts) | - |

#### Returns

`Promise`\<`string`\>

___

### rootfs

**rootfs**(): [`Directory`](api_client_gen.Directory.md)

Retrieves this container's root filesystem. Mounts are not included.

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### shellEndpoint

**shellEndpoint**(): `Promise`\<`string`\>

Return a websocket endpoint that, if connected to, will start the container with a TTY streamed
over the websocket.

Primarily intended for internal use with the dagger CLI.

#### Returns

`Promise`\<`string`\>

___

### stderr

**stderr**(): `Promise`\<`string`\>

The error stream of the last executed command.

Will execute default command if none is set, or error if there's no default.

#### Returns

`Promise`\<`string`\>

___

### stdout

**stdout**(): `Promise`\<`string`\>

The output stream of the last executed command.

Will execute default command if none is set, or error if there's no default.

#### Returns

`Promise`\<`string`\>

___

### sync

**sync**(): `Promise`\<[`Container`](api_client_gen.Container.md)\>

Forces evaluation of the pipeline in the engine.

It doesn't run the default command if no exec has been set.

#### Returns

`Promise`\<[`Container`](api_client_gen.Container.md)\>

___

### user

**user**(): `Promise`\<`string`\>

Retrieves the user to be set for all commands.

#### Returns

`Promise`\<`string`\>

___

### with

**with**(`arg`): [`Container`](api_client_gen.Container.md)

Call the provided function with current Container.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`Container`](api_client_gen.Container.md)) => [`Container`](api_client_gen.Container.md) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withDefaultArgs

**withDefaultArgs**(`args`): [`Container`](api_client_gen.Container.md)

Configures default arguments for future commands.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `args` | `string`[] | Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withDirectory

**withDirectory**(`path`, `directory`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a directory written at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the written directory (e.g., "/tmp/directory"). |
| `directory` | [`Directory`](api_client_gen.Directory.md) | Identifier of the directory to write |
| `opts?` | [`ContainerWithDirectoryOpts`](../modules/api_client_gen.md#containerwithdirectoryopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withEntrypoint

**withEntrypoint**(`args`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container but with a different command entrypoint.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `args` | `string`[] | Entrypoint to use for future executions (e.g., ["go", "run"]). |
| `opts?` | [`ContainerWithEntrypointOpts`](../modules/api_client_gen.md#containerwithentrypointopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withEnvVariable

**withEnvVariable**(`name`, `value`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus the given environment variable.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the environment variable (e.g., "HOST"). |
| `value` | `string` | The value of the environment variable. (e.g., "localhost"). |
| `opts?` | [`ContainerWithEnvVariableOpts`](../modules/api_client_gen.md#containerwithenvvariableopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withExec

**withExec**(`args`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container after executing the specified command inside it.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `args` | `string`[] | Command to run instead of the container's default command (e.g., ["run", "main.go"]). If empty, the container's default command is used. |
| `opts?` | [`ContainerWithExecOpts`](../modules/api_client_gen.md#containerwithexecopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withExposedPort

**withExposedPort**(`port`, `opts?`): [`Container`](api_client_gen.Container.md)

Expose a network port.

Exposed ports serve two purposes:

- For health checks and introspection, when running services
- For setting the EXPOSE OCI field when publishing the container

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `port` | `number` | Port number to expose |
| `opts?` | [`ContainerWithExposedPortOpts`](../modules/api_client_gen.md#containerwithexposedportopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withFile

**withFile**(`path`, `source`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus the contents of the given file copied to the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the copied file (e.g., "/tmp/file.txt"). |
| `source` | [`File`](api_client_gen.File.md) | Identifier of the file to copy. |
| `opts?` | [`ContainerWithFileOpts`](../modules/api_client_gen.md#containerwithfileopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withFocus

**withFocus**(): [`Container`](api_client_gen.Container.md)

Indicate that subsequent operations should be featured more prominently in
the UI.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withLabel

**withLabel**(`name`, `value`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus the given label.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the label (e.g., "org.opencontainers.artifact.created"). |
| `value` | `string` | The value of the label (e.g., "2023-01-01T00:00:00Z"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withMountedCache

**withMountedCache**(`path`, `cache`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a cache volume mounted at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the cache directory (e.g., "/cache/node_modules"). |
| `cache` | [`CacheVolume`](api_client_gen.CacheVolume.md) | Identifier of the cache volume to mount. |
| `opts?` | [`ContainerWithMountedCacheOpts`](../modules/api_client_gen.md#containerwithmountedcacheopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withMountedDirectory

**withMountedDirectory**(`path`, `source`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a directory mounted at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the mounted directory (e.g., "/mnt/directory"). |
| `source` | [`Directory`](api_client_gen.Directory.md) | Identifier of the mounted directory. |
| `opts?` | [`ContainerWithMountedDirectoryOpts`](../modules/api_client_gen.md#containerwithmounteddirectoryopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withMountedFile

**withMountedFile**(`path`, `source`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a file mounted at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the mounted file (e.g., "/tmp/file.txt"). |
| `source` | [`File`](api_client_gen.File.md) | Identifier of the mounted file. |
| `opts?` | [`ContainerWithMountedFileOpts`](../modules/api_client_gen.md#containerwithmountedfileopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withMountedSecret

**withMountedSecret**(`path`, `source`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a secret mounted into a file at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the secret file (e.g., "/tmp/secret.txt"). |
| `source` | [`Secret`](api_client_gen.Secret.md) | Identifier of the secret to mount. |
| `opts?` | [`ContainerWithMountedSecretOpts`](../modules/api_client_gen.md#containerwithmountedsecretopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withMountedTemp

**withMountedTemp**(`path`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a temporary directory mounted at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the temporary directory (e.g., "/tmp/temp_dir"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withNewFile

**withNewFile**(`path`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a new file written at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the written file (e.g., "/tmp/file.txt"). |
| `opts?` | [`ContainerWithNewFileOpts`](../modules/api_client_gen.md#containerwithnewfileopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withRegistryAuth

**withRegistryAuth**(`address`, `username`, `secret`): [`Container`](api_client_gen.Container.md)

Retrieves this container with a registry authentication for a given address.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `address` | `string` | Registry's address to bind the authentication to. Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main). |
| `username` | `string` | The username of the registry's account (e.g., "Dagger"). |
| `secret` | [`Secret`](api_client_gen.Secret.md) | The API key, password or token to authenticate to this registry. |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withRootfs

**withRootfs**(`directory`): [`Container`](api_client_gen.Container.md)

Initializes this container from this DirectoryID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `directory` | [`Directory`](api_client_gen.Directory.md) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withSecretVariable

**withSecretVariable**(`name`, `secret`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus an env variable containing the given secret.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the secret variable (e.g., "API_SECRET"). |
| `secret` | [`Secret`](api_client_gen.Secret.md) | The identifier of the secret value. |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withServiceBinding

**withServiceBinding**(`alias`, `service`): [`Container`](api_client_gen.Container.md)

Establish a runtime dependency on a service.

The service will be started automatically when needed and detached when it is
no longer needed, executing the default command if none is set.

The service will be reachable from the container via the provided hostname alias.

The service dependency will also convey to any files or directories produced by the container.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `alias` | `string` | A name that can be used to reach the service from the container |
| `service` | [`Service`](api_client_gen.Service.md) | Identifier of the service container |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withUnixSocket

**withUnixSocket**(`path`, `source`, `opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container plus a socket forwarded to the given Unix socket path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the forwarded Unix socket (e.g., "/tmp/socket"). |
| `source` | [`Socket`](api_client_gen.Socket.md) | Identifier of the socket to forward. |
| `opts?` | [`ContainerWithUnixSocketOpts`](../modules/api_client_gen.md#containerwithunixsocketopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withUser

**withUser**(`name`): [`Container`](api_client_gen.Container.md)

Retrieves this container with a different command user.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The user to set (e.g., "root"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withWorkdir

**withWorkdir**(`path`): [`Container`](api_client_gen.Container.md)

Retrieves this container with a different working directory.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | The path to set as the working directory (e.g., "/app"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutDefaultArgs

**withoutDefaultArgs**(): [`Container`](api_client_gen.Container.md)

Retrieves this container with unset default arguments for future commands.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutEntrypoint

**withoutEntrypoint**(`opts?`): [`Container`](api_client_gen.Container.md)

Retrieves this container with an unset command entrypoint.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ContainerWithoutEntrypointOpts`](../modules/api_client_gen.md#containerwithoutentrypointopts) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutEnvVariable

**withoutEnvVariable**(`name`): [`Container`](api_client_gen.Container.md)

Retrieves this container minus the given environment variable.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the environment variable (e.g., "HOST"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutExposedPort

**withoutExposedPort**(`port`, `opts?`): [`Container`](api_client_gen.Container.md)

Unexpose a previously exposed port.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `port` | `number` | Port number to unexpose |
| `opts?` | [`ContainerWithoutExposedPortOpts`](../modules/api_client_gen.md#containerwithoutexposedportopts) | - |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutFocus

**withoutFocus**(): [`Container`](api_client_gen.Container.md)

Indicate that subsequent operations should not be featured more prominently
in the UI.

This is the initial state of all containers.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutLabel

**withoutLabel**(`name`): [`Container`](api_client_gen.Container.md)

Retrieves this container minus the given environment label.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the label to remove (e.g., "org.opencontainers.artifact.created"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutMount

**withoutMount**(`path`): [`Container`](api_client_gen.Container.md)

Retrieves this container after unmounting everything at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the cache directory (e.g., "/cache/node_modules"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutRegistryAuth

**withoutRegistryAuth**(`address`): [`Container`](api_client_gen.Container.md)

Retrieves this container without the registry authentication of a given address.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `address` | `string` | Registry's address to remove the authentication from. Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutUnixSocket

**withoutUnixSocket**(`path`): [`Container`](api_client_gen.Container.md)

Retrieves this container with a previously added Unix socket removed.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the socket to remove (e.g., "/tmp/socket"). |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutUser

**withoutUser**(): [`Container`](api_client_gen.Container.md)

Retrieves this container with an unset command user.

Should default to root.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### withoutWorkdir

**withoutWorkdir**(): [`Container`](api_client_gen.Container.md)

Retrieves this container with an unset working directory.

Should default to "/".

#### Returns

[`Container`](api_client_gen.Container.md)

___

### workdir

**workdir**(): `Promise`\<`string`\>

Retrieves the working directory for all commands.

#### Returns

`Promise`\<`string`\>
