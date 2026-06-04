---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerExportImageOpts

> **ContainerExportImageOpts** = `object`

## Properties

### forcedCompression?

> `optional` **forcedCompression?**: [`ImageLayerCompression`](../enumerations/ImageLayerCompression.md)

Force each layer of the exported image to use the specified compression algorithm.

If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.

***

### mediaTypes?

> `optional` **mediaTypes?**: [`ImageMediaTypes`](../enumerations/ImageMediaTypes.md)

Use the specified media types for the exported image's layers.

Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.

***

### platformVariants?

> `optional` **platformVariants?**: [`Container`](../classes/Container.md)[]

Identifiers for other platform specific containers.

Used for multi-platform image.
