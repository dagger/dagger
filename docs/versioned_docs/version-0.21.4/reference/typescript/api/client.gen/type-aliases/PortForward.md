---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

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
