[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Service

# Class: Service

A content-addressed service providing TCP connectivity.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Service**(`ctx?`, `_id?`, `_endpoint?`, `_hostname?`, `_start?`, `_stop?`, `_sync?`, `_up?`): `Service`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ServiceID`](../type-aliases/ServiceID.md)

##### \_endpoint?

`string`

##### \_hostname?

`string`

##### \_start?

[`ServiceID`](../type-aliases/ServiceID.md)

##### \_stop?

[`ServiceID`](../type-aliases/ServiceID.md)

##### \_sync?

[`ServiceID`](../type-aliases/ServiceID.md)

##### \_up?

[`Void`](../type-aliases/Void.md)

#### Returns

`Service`

#### Overrides

`BaseClient.constructor`

## Methods

### endpoint()

> **endpoint**(`opts?`): `Promise`\<`string`\>

Retrieves an endpoint that clients can use to reach this container.

If no port is specified, the first exposed port is used. If none exist an error is returned.

If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.

#### Parameters

##### opts?

[`ServiceEndpointOpts`](../type-aliases/ServiceEndpointOpts.md)

#### Returns

`Promise`\<`string`\>

***

### hostname()

> **hostname**(): `Promise`\<`string`\>

Retrieves a hostname which can be used by clients to reach this container.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ServiceID`](../type-aliases/ServiceID.md)\>

A unique identifier for this Service.

#### Returns

`Promise`\<[`ServiceID`](../type-aliases/ServiceID.md)\>

***

### ports()

> **ports**(): `Promise`\<[`Port`](Port.md)[]\>

Retrieves the list of ports provided by the service.

#### Returns

`Promise`\<[`Port`](Port.md)[]\>

***

### start()

> **start**(): `Promise`\<`Service`\>

Start the service and wait for its health checks to succeed.

Services bound to a Container do not need to be manually started.

#### Returns

`Promise`\<`Service`\>

***

### stop()

> **stop**(`opts?`): `Promise`\<`Service`\>

Stop the service.

#### Parameters

##### opts?

[`ServiceStopOpts`](../type-aliases/ServiceStopOpts.md)

#### Returns

`Promise`\<`Service`\>

***

### sync()

> **sync**(): `Promise`\<`Service`\>

Forces evaluation of the pipeline in the engine.

#### Returns

`Promise`\<`Service`\>

***

### terminal()

> **terminal**(`opts?`): `Service`

#### Parameters

##### opts?

[`ServiceTerminalOpts`](../type-aliases/ServiceTerminalOpts.md)

#### Returns

`Service`

***

### up()

> **up**(`opts?`): `Promise`\<`void`\>

Creates a tunnel that forwards traffic from the caller's network to this service.

#### Parameters

##### opts?

[`ServiceUpOpts`](../type-aliases/ServiceUpOpts.md)

#### Returns

`Promise`\<`void`\>

***

### with()

> **with**(`arg`): `Service`

Call the provided function with current Service.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Service`

#### Returns

`Service`

***

### withHostname()

> **withHostname**(`hostname`): `Service`

Configures a hostname which can be used by clients within the session to reach this container.

#### Parameters

##### hostname

`string`

The hostname to use.

#### Returns

`Service`
