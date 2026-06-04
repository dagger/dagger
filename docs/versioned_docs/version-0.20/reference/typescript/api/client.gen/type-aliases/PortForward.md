[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / PortForward

# Type Alias: PortForward

> **PortForward** = `object`

## Properties

### backend

> **backend**: `number`

Destination port for traffic.

***

### frontend?

> `optional` **frontend?**: `number`

Port to expose to clients. If unspecified, a default will be chosen.

***

### protocol?

> `optional` **protocol?**: [`NetworkProtocol`](../enumerations/NetworkProtocol.md)

Transport layer protocol to use for traffic.
