[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Host

# Class: Host

Information about the host environment.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Host**(`ctx?`, `_id?`, `_findUp?`): `Host`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`HostID`](../type-aliases/HostID.md)

##### \_findUp?

`string`

#### Returns

`Host`

#### Overrides

`BaseClient.constructor`

## Methods

### containerImage()

> **containerImage**(`name`): [`Container`](Container.md)

Accesses a container image on the host.

#### Parameters

##### name

`string`

Name of the image to access.

#### Returns

[`Container`](Container.md)

***

### directory()

> **directory**(`path`, `opts?`): [`Directory`](Directory.md)

Accesses a directory on the host.

#### Parameters

##### path

`string`

Location of the directory to access (e.g., ".").

##### opts?

[`HostDirectoryOpts`](../type-aliases/HostDirectoryOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### file()

> **file**(`path`, `opts?`): [`File`](File.md)

Accesses a file on the host.

#### Parameters

##### path

`string`

Location of the file to retrieve (e.g., "README.md").

##### opts?

[`HostFileOpts`](../type-aliases/HostFileOpts.md)

#### Returns

[`File`](File.md)

***

### findUp()

> **findUp**(`name`, `opts?`): `Promise`\<`string`\>

Search for a file or directory by walking up the tree from system workdir. Return its relative path. If no match, return null

#### Parameters

##### name

`string`

name of the file or directory to search for

##### opts?

[`HostFindUpOpts`](../type-aliases/HostFindUpOpts.md)

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`HostID`](../type-aliases/HostID.md)\>

A unique identifier for this Host.

#### Returns

`Promise`\<[`HostID`](../type-aliases/HostID.md)\>

***

### service()

> **service**(`ports`, `opts?`): [`Service`](Service.md)

Creates a service that forwards traffic to a specified address via the host.

#### Parameters

##### ports

[`PortForward`](../type-aliases/PortForward.md)[]

Ports to expose via the service, forwarding through the host network.

If a port's frontend is unspecified or 0, it defaults to the same as the backend port.

An empty set of ports is not valid; an error will be returned.

##### opts?

[`HostServiceOpts`](../type-aliases/HostServiceOpts.md)

#### Returns

[`Service`](Service.md)

***

### tunnel()

> **tunnel**(`service`, `opts?`): [`Service`](Service.md)

Creates a tunnel that forwards traffic from the host to a service.

#### Parameters

##### service

[`Service`](Service.md)

Service to send traffic from the tunnel.

##### opts?

[`HostTunnelOpts`](../type-aliases/HostTunnelOpts.md)

#### Returns

[`Service`](Service.md)

***

### unixSocket()

> **unixSocket**(`path`): [`Socket`](Socket.md)

Accesses a Unix socket on the host.

#### Parameters

##### path

`string`

Location of the Unix socket (e.g., "/var/run/docker.sock").

#### Returns

[`Socket`](Socket.md)
