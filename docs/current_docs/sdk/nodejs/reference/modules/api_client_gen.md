---
id: "api_client_gen"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

## Enumerations

- [CacheSharingMode](../enums/api_client_gen.CacheSharingMode.md)
- [ImageLayerCompression](../enums/api_client_gen.ImageLayerCompression.md)
- [ImageMediaTypes](../enums/api_client_gen.ImageMediaTypes.md)
- [NetworkProtocol](../enums/api_client_gen.NetworkProtocol.md)
- [TypeDefKind](../enums/api_client_gen.TypeDefKind.md)

## Classes

- [CacheVolume](../classes/api_client_gen.CacheVolume.md)
- [Client](../classes/api_client_gen.Client.md)
- [Container](../classes/api_client_gen.Container.md)
- [Directory](../classes/api_client_gen.Directory.md)
- [EnvVariable](../classes/api_client_gen.EnvVariable.md)
- [FieldTypeDef](../classes/api_client_gen.FieldTypeDef.md)
- [File](../classes/api_client_gen.File.md)
- [FunctionArg](../classes/api_client_gen.FunctionArg.md)
- [FunctionCall](../classes/api_client_gen.FunctionCall.md)
- [FunctionCallArgValue](../classes/api_client_gen.FunctionCallArgValue.md)
- [Function\_](../classes/api_client_gen.Function_.md)
- [GeneratedCode](../classes/api_client_gen.GeneratedCode.md)
- [GitRef](../classes/api_client_gen.GitRef.md)
- [GitRepository](../classes/api_client_gen.GitRepository.md)
- [Host](../classes/api_client_gen.Host.md)
- [InterfaceTypeDef](../classes/api_client_gen.InterfaceTypeDef.md)
- [Label](../classes/api_client_gen.Label.md)
- [ListTypeDef](../classes/api_client_gen.ListTypeDef.md)
- [ModuleConfig](../classes/api_client_gen.ModuleConfig.md)
- [Module\_](../classes/api_client_gen.Module_.md)
- [ObjectTypeDef](../classes/api_client_gen.ObjectTypeDef.md)
- [Port](../classes/api_client_gen.Port.md)
- [Secret](../classes/api_client_gen.Secret.md)
- [Service](../classes/api_client_gen.Service.md)
- [Socket](../classes/api_client_gen.Socket.md)
- [TypeDef](../classes/api_client_gen.TypeDef.md)

## Type Aliases

### BuildArg

 **BuildArg**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The build argument name. |
| `value` | `string` | The build argument value. |

___

### CacheVolumeID

 **CacheVolumeID**: `string` & \{ `__CacheVolumeID`: `never`  }

A global cache volume identifier.

___

### ClientContainerOpts

 **ClientContainerOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `id?` | [`ContainerID`](api_client_gen.md#containerid) |
| `platform?` | [`Platform`](api_client_gen.md#platform) |

___

### ClientDirectoryOpts

 **ClientDirectoryOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `id?` | [`DirectoryID`](api_client_gen.md#directoryid) |

___

### ClientGitOpts

 **ClientGitOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `experimentalServiceHost?` | [`Service`](../classes/api_client_gen.Service.md) | A service which must be started before the repo is fetched. |
| `keepGitDir?` | `boolean` | Set to true to keep .git directory. |
| `sshAuthSocket?` | [`Socket`](../classes/api_client_gen.Socket.md) | Set SSH auth socket |
| `sshKnownHosts?` | `string` | Set SSH known hosts |

___

### ClientHttpOpts

 **ClientHttpOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `experimentalServiceHost?` | [`Service`](../classes/api_client_gen.Service.md) | A service which must be started before the URL is fetched. |

___

### ClientModuleConfigOpts

 **ClientModuleConfigOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `subpath?` | `string` |

___

### ClientPipelineOpts

 **ClientPipelineOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `description?` | `string` | Pipeline description. |
| `labels?` | [`PipelineLabel`](api_client_gen.md#pipelinelabel)[] | Pipeline labels. |

___

### ClientSocketOpts

 **ClientSocketOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `id?` | [`SocketID`](api_client_gen.md#socketid) |

___

### ContainerAsTarballOpts

 **ContainerAsTarballOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `forcedCompression?` | [`ImageLayerCompression`](../enums/api_client_gen.ImageLayerCompression.md) | Force each layer of the image to use the specified compression algorithm. If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip. |
| `mediaTypes?` | [`ImageMediaTypes`](../enums/api_client_gen.ImageMediaTypes.md) | Use the specified media types for the image's layers. Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support. |
| `platformVariants?` | [`Container`](../classes/api_client_gen.Container.md)[] | Identifiers for other platform specific containers. Used for multi-platform image. |

___

### ContainerBuildOpts

 **ContainerBuildOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `buildArgs?` | [`BuildArg`](api_client_gen.md#buildarg)[] | Additional build arguments. |
| `dockerfile?` | `string` | Path to the Dockerfile to use. Default: './Dockerfile'. |
| `secrets?` | [`Secret`](../classes/api_client_gen.Secret.md)[] | Secrets to pass to the build. They will be mounted at /run/secrets/[secret-name] in the build container They can be accessed in the Dockerfile using the "secret" mount type and mount path /run/secrets/[secret-name] e.g. RUN --mount=type=secret,id=my-secret curl url?token=$(cat /run/secrets/my-secret)" |
| `target?` | `string` | Target build stage to build. |

___

### ContainerExportOpts

 **ContainerExportOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `forcedCompression?` | [`ImageLayerCompression`](../enums/api_client_gen.ImageLayerCompression.md) | Force each layer of the exported image to use the specified compression algorithm. If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip. |
| `mediaTypes?` | [`ImageMediaTypes`](../enums/api_client_gen.ImageMediaTypes.md) | Use the specified media types for the exported image's layers. Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support. |
| `platformVariants?` | [`Container`](../classes/api_client_gen.Container.md)[] | Identifiers for other platform specific containers. Used for multi-platform image. |

___

### ContainerID

 **ContainerID**: `string` & \{ `__ContainerID`: `never`  }

A unique container identifier. Null designates an empty container (scratch).

___

### ContainerImportOpts

 **ContainerImportOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `tag?` | `string` | Identifies the tag to import from the archive, if the archive bundles multiple tags. |

___

### ContainerPipelineOpts

 **ContainerPipelineOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `description?` | `string` | Pipeline description. |
| `labels?` | [`PipelineLabel`](api_client_gen.md#pipelinelabel)[] | Pipeline labels. |

___

### ContainerPublishOpts

 **ContainerPublishOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `forcedCompression?` | [`ImageLayerCompression`](../enums/api_client_gen.ImageLayerCompression.md) | Force each layer of the published image to use the specified compression algorithm. If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip. |
| `mediaTypes?` | [`ImageMediaTypes`](../enums/api_client_gen.ImageMediaTypes.md) | Use the specified media types for the published image's layers. Defaults to OCI, which is largely compatible with most recent registries, but Docker may be needed for older registries without OCI support. |
| `platformVariants?` | [`Container`](../classes/api_client_gen.Container.md)[] | Identifiers for other platform specific containers. Used for multi-platform image. |

___

### ContainerWithDirectoryOpts

 **ContainerWithDirectoryOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `exclude?` | `string`[] | Patterns to exclude in the written directory (e.g., ["node_modules/**", ".gitignore", ".git/"]). |
| `include?` | `string`[] | Patterns to include in the written directory (e.g., ["*.go", "go.mod", "go.sum"]). |
| `owner?` | `string` | A user:group to set for the directory and its contents. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |

___

### ContainerWithEntrypointOpts

 **ContainerWithEntrypointOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `keepDefaultArgs?` | `boolean` | Don't remove the default arguments when setting the entrypoint. |

___

### ContainerWithEnvVariableOpts

 **ContainerWithEnvVariableOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `expand?` | `boolean` | Replace `${VAR}` or $VAR in the value according to the current environment variables defined in the container (e.g., "/opt/bin:$PATH"). |

___

### ContainerWithExecOpts

 **ContainerWithExecOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `experimentalPrivilegedNesting?` | `boolean` | Provides dagger access to the executed command. Do not use this option unless you trust the command being executed. The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM. |
| `insecureRootCapabilities?` | `boolean` | Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing `docker run` with the `--privileged` flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands. |
| `redirectStderr?` | `string` | Redirect the command's standard error to a file in the container (e.g., "/tmp/stderr"). |
| `redirectStdout?` | `string` | Redirect the command's standard output to a file in the container (e.g., "/tmp/stdout"). |
| `skipEntrypoint?` | `boolean` | If the container has an entrypoint, ignore it for args rather than using it to wrap them. |
| `stdin?` | `string` | Content to write to the command's standard input before closing (e.g., "Hello world"). |

___

### ContainerWithExposedPortOpts

 **ContainerWithExposedPortOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `description?` | `string` | Optional port description |
| `protocol?` | [`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md) | Transport layer network protocol |

___

### ContainerWithFileOpts

 **ContainerWithFileOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `owner?` | `string` | A user:group to set for the file. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |
| `permissions?` | `number` | Permission given to the copied file (e.g., 0600). Default: 0644. |

___

### ContainerWithMountedCacheOpts

 **ContainerWithMountedCacheOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `owner?` | `string` | A user:group to set for the mounted cache directory. Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |
| `sharing?` | [`CacheSharingMode`](../enums/api_client_gen.CacheSharingMode.md) | Sharing mode of the cache volume. |
| `source?` | [`Directory`](../classes/api_client_gen.Directory.md) | Identifier of the directory to use as the cache volume's root. |

___

### ContainerWithMountedDirectoryOpts

 **ContainerWithMountedDirectoryOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `owner?` | `string` | A user:group to set for the mounted directory and its contents. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |

___

### ContainerWithMountedFileOpts

 **ContainerWithMountedFileOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `owner?` | `string` | A user or user:group to set for the mounted file. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |

___

### ContainerWithMountedSecretOpts

 **ContainerWithMountedSecretOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `mode?` | `number` | Permission given to the mounted secret (e.g., 0600). This option requires an owner to be set to be active. Default: 0400. |
| `owner?` | `string` | A user:group to set for the mounted secret. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |

___

### ContainerWithNewFileOpts

 **ContainerWithNewFileOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `contents?` | `string` | Content of the file to write (e.g., "Hello world!"). |
| `owner?` | `string` | A user:group to set for the file. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |
| `permissions?` | `number` | Permission given to the written file (e.g., 0600). Default: 0644. |

___

### ContainerWithUnixSocketOpts

 **ContainerWithUnixSocketOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `owner?` | `string` | A user:group to set for the mounted socket. The user and group can either be an ID (1000:1000) or a name (foo:bar). If the group is omitted, it defaults to the same as the user. |

___

### ContainerWithoutEntrypointOpts

 **ContainerWithoutEntrypointOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `keepDefaultArgs?` | `boolean` | Don't remove the default arguments when unsetting the entrypoint. |

___

### ContainerWithoutExposedPortOpts

 **ContainerWithoutExposedPortOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `protocol?` | [`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md) | Port protocol to unexpose |

___

### DateTime

 **DateTime**: `string` & \{ `__DateTime`: `never`  }

The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string

___

### DirectoryAsModuleOpts

 **DirectoryAsModuleOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `sourceSubpath?` | `string` | An optional subpath of the directory which contains the module's source code. This is needed when the module code is in a subdirectory but requires parent directories to be loaded in order to execute. For example, the module source code may need a go.mod, project.toml, package.json, etc. file from a parent directory. If not set, the module source code is loaded from the root of the directory. |

___

### DirectoryDockerBuildOpts

 **DirectoryDockerBuildOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `buildArgs?` | [`BuildArg`](api_client_gen.md#buildarg)[] | Build arguments to use in the build. |
| `dockerfile?` | `string` | Path to the Dockerfile to use (e.g., "frontend.Dockerfile"). Defaults: './Dockerfile'. |
| `platform?` | [`Platform`](api_client_gen.md#platform) | The platform to build. |
| `secrets?` | [`Secret`](../classes/api_client_gen.Secret.md)[] | Secrets to pass to the build. They will be mounted at /run/secrets/[secret-name]. |
| `target?` | `string` | Target build stage to build. |

___

### DirectoryEntriesOpts

 **DirectoryEntriesOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `path?` | `string` | Location of the directory to look at (e.g., "/src"). |

___

### DirectoryID

 **DirectoryID**: `string` & \{ `__DirectoryID`: `never`  }

A content-addressed directory identifier.

___

### DirectoryPipelineOpts

 **DirectoryPipelineOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `description?` | `string` | Pipeline description. |
| `labels?` | [`PipelineLabel`](api_client_gen.md#pipelinelabel)[] | Pipeline labels. |

___

### DirectoryWithDirectoryOpts

 **DirectoryWithDirectoryOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `exclude?` | `string`[] | Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]). |
| `include?` | `string`[] | Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]). |

___

### DirectoryWithFileOpts

 **DirectoryWithFileOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `permissions?` | `number` | Permission given to the copied file (e.g., 0600). Default: 0644. |

___

### DirectoryWithNewDirectoryOpts

 **DirectoryWithNewDirectoryOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `permissions?` | `number` | Permission granted to the created directory (e.g., 0777). Default: 0755. |

___

### DirectoryWithNewFileOpts

 **DirectoryWithNewFileOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `permissions?` | `number` | Permission given to the copied file (e.g., 0600). Default: 0644. |

___

### FileExportOpts

 **FileExportOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `allowParentDirPath?` | `boolean` | If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory. |

___

### FileID

 **FileID**: `string` & \{ `__FileID`: `never`  }

A file identifier.

___

### FunctionArgID

 **FunctionArgID**: `string` & \{ `__FunctionArgID`: `never`  }

A reference to a FunctionArg.

___

### FunctionID

 **FunctionID**: `string` & \{ `__FunctionID`: `never`  }

A reference to a Function.

___

### FunctionWithArgOpts

 **FunctionWithArgOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `defaultValue?` | [`JSON`](api_client_gen.md#json) | A default value to use for this argument if not explicitly set by the caller, if any |
| `description?` | `string` | A doc string for the argument, if any |

___

### GeneratedCodeID

 **GeneratedCodeID**: `string` & \{ `__GeneratedCodeID`: `never`  }

A reference to GeneratedCode.

___

### GitRefID

 **GitRefID**: `string` & \{ `__GitRefID`: `never`  }

A git reference identifier.

___

### GitRefTreeOpts

 **GitRefTreeOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `sshAuthSocket?` | [`Socket`](../classes/api_client_gen.Socket.md) |
| `sshKnownHosts?` | `string` |

___

### GitRepositoryID

 **GitRepositoryID**: `string` & \{ `__GitRepositoryID`: `never`  }

A git repository identifier.

___

### HostDirectoryOpts

 **HostDirectoryOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `exclude?` | `string`[] | Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]). |
| `include?` | `string`[] | Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]). |

___

### HostServiceOpts

 **HostServiceOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `host?` | `string` | Upstream host to forward traffic to. |

___

### HostTunnelOpts

 **HostTunnelOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `native?` | `boolean` | Map each service port to the same port on the host, as if the service were running natively. Note: enabling may result in port conflicts. |
| `ports?` | [`PortForward`](api_client_gen.md#portforward)[] | Configure explicit port forwarding rules for the tunnel. If a port's frontend is unspecified or 0, a random port will be chosen by the host. If no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host. If ports are given and native is true, the ports are additive. |

___

### ID

 **ID**: `string` & \{ `__ID`: `never`  }

The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `"4"`) or integer (such as `4`) input value will be accepted as an ID.

___

### JSON

 **JSON**: `string` & \{ `__JSON`: `never`  }

An arbitrary JSON-encoded value.

___

### ModuleID

 **ModuleID**: `string` & \{ `__ModuleID`: `never`  }

A reference to a Module.

___

### PipelineLabel

 **PipelineLabel**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Label name. |
| `value` | `string` | Label value. |

___

### Platform

 **Platform**: `string` & \{ `__Platform`: `never`  }

The platform config OS and architecture in a Container.

The format is [os]/[platform]/[version] (e.g., "darwin/arm64/v7", "windows/amd64", "linux/arm64").

___

### PortForward

 **PortForward**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `backend` | `number` | Destination port for traffic. |
| `frontend?` | `number` | Port to expose to clients. If unspecified, a default will be chosen. |
| `protocol?` | [`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md) | Protocol to use for traffic. |

___

### SecretID

 **SecretID**: `string` & \{ `__SecretID`: `never`  }

A unique identifier for a secret.

___

### ServiceEndpointOpts

 **ServiceEndpointOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `port?` | `number` | The exposed port number for the endpoint |
| `scheme?` | `string` | Return a URL with the given scheme, eg. http for http:// |

___

### ServiceID

 **ServiceID**: `string` & \{ `__ServiceID`: `never`  }

A unique service identifier.

___

### SocketID

 **SocketID**: `string` & \{ `__SocketID`: `never`  }

A content-addressed socket identifier.

___

### TypeDefID

 **TypeDefID**: `string` & \{ `__TypeDefID`: `never`  }

A reference to a TypeDef.

___

### TypeDefWithFieldOpts

 **TypeDefWithFieldOpts**: `Object`

#### Type declaration

| Name | Type | Description |
| :------ | :------ | :------ |
| `description?` | `string` | A doc string for the field, if any |

___

### TypeDefWithInterfaceOpts

 **TypeDefWithInterfaceOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `description?` | `string` |

___

### TypeDefWithObjectOpts

 **TypeDefWithObjectOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `description?` | `string` |

___

### Void

 **Void**: `string` & \{ `__Void`: `never`  }

The absense of a value.

A Null Void is used as a placeholder for resolvers that do not return anything.

___

### \_\_TypeEnumValuesOpts

 **\_\_TypeEnumValuesOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `includeDeprecated?` | `boolean` |

___

### \_\_TypeFieldsOpts

 **\_\_TypeFieldsOpts**: `Object`

#### Type declaration

| Name | Type |
| :------ | :------ |
| `includeDeprecated?` | `boolean` |

## Variables

### dag

 `Const` **dag**: [`Client`](../classes/api_client_gen.Client.md)
