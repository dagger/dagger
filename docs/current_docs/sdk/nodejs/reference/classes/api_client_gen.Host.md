---
id: "api_client_gen.Host"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Host

Information about the host execution environment.

## Hierarchy

- `BaseClient`

  ↳ **`Host`**

## Constructors

### constructor

**new Host**(`parent?`): [`Host`](api_client_gen.Host.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |

#### Returns

[`Host`](api_client_gen.Host.md)

#### Overrides

BaseClient.constructor

## Methods

### directory

**directory**(`path`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Accesses a directory on the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the directory to access (e.g., "."). |
| `opts?` | [`HostDirectoryOpts`](../modules/api_client_gen.md#hostdirectoryopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### file

**file**(`path`): [`File`](api_client_gen.File.md)

Accesses a file on the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the file to retrieve (e.g., "README.md"). |

#### Returns

[`File`](api_client_gen.File.md)

___

### service

**service**(`ports`, `opts?`): [`Service`](api_client_gen.Service.md)

Creates a service that forwards traffic to a specified address via the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `ports` | [`PortForward`](../modules/api_client_gen.md#portforward)[] | Ports to expose via the service, forwarding through the host network. If a port's frontend is unspecified or 0, it defaults to the same as the backend port. An empty set of ports is not valid; an error will be returned. |
| `opts?` | [`HostServiceOpts`](../modules/api_client_gen.md#hostserviceopts) | - |

#### Returns

[`Service`](api_client_gen.Service.md)

___

### setSecretFile

**setSecretFile**(`name`, `path`): [`Secret`](api_client_gen.Secret.md)

Sets a secret given a user-defined name and the file path on the host, and returns the secret.
The file is limited to a size of 512000 bytes.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The user defined name for this secret. |
| `path` | `string` | Location of the file to set as a secret. |

#### Returns

[`Secret`](api_client_gen.Secret.md)

___

### tunnel

**tunnel**(`service`, `opts?`): [`Service`](api_client_gen.Service.md)

Creates a tunnel that forwards traffic from the host to a service.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `service` | [`Service`](api_client_gen.Service.md) | Service to send traffic from the tunnel. |
| `opts?` | [`HostTunnelOpts`](../modules/api_client_gen.md#hosttunnelopts) | - |

#### Returns

[`Service`](api_client_gen.Service.md)

___

### unixSocket

**unixSocket**(`path`): [`Socket`](api_client_gen.Socket.md)

Accesses a Unix socket on the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the Unix socket (e.g., "/var/run/docker.sock"). |

#### Returns

[`Socket`](api_client_gen.Socket.md)
