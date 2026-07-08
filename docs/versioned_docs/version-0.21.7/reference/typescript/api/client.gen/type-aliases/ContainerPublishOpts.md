---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerPublishOpts

> **ContainerPublishOpts** = `object`

## Properties

### forcedCompression?

> `optional` **forcedCompression?**: [`ImageLayerCompression`](../enumerations/ImageLayerCompression.md)

Force each layer of the published image to use the specified compression algorithm.

If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.

***

### mediaTypes?

> `optional` **mediaTypes?**: [`ImageMediaTypes`](../enumerations/ImageMediaTypes.md)

Use the specified media types for the published image's layers.

Defaults to "OCI", which is compatible with most recent registries, but "Docker" may be needed for older registries without OCI support.

***

### platformVariants?

> `optional` **platformVariants?**: [`Container`](../classes/Container.md)[]

Identifiers for other platform specific containers.

Used for multi-platform image.

***

### registryService?

> `optional` **registryService?**: [`Service`](../classes/Service.md)

Service to use as the registry endpoint for the image address.

The service will be started only for this push.
