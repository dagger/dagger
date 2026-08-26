[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ServiceUpOpts

# Type Alias: ServiceUpOpts

> **ServiceUpOpts** = `object`

## Properties

### ports?

> `optional` **ports?**: [`PortForward`](PortForward.md)[]

List of frontend/backend port mappings to forward.

Frontend is the port accepting traffic on the host, backend is the service port.

***

### random?

> `optional` **random?**: `boolean`

Bind each tunnel port to a random port on the host.
