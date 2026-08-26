[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / CacheSharingMode

# Enumeration: CacheSharingMode

Sharing mode of the cache volume.

## Enumeration Members

### Locked

> **Locked**: `"LOCKED"`

Shares the cache volume amongst many build pipelines, but will serialize the writes

***

### Private

> **Private**: `"PRIVATE"`

Keeps a cache volume for a single build pipeline

***

### Shared

> **Shared**: `"SHARED"`

Shares the cache volume amongst many build pipelines
