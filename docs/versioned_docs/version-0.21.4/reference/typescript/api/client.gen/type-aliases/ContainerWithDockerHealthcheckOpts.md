---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerWithDockerHealthcheckOpts

> **ContainerWithDockerHealthcheckOpts** = `object`

## Properties

### interval?

> `optional` **interval?**: `string`

Interval between running healthcheck. Example: "30s"

***

### retries?

> `optional` **retries?**: `number`

The maximum number of consecutive failures before the container is marked as unhealthy. Example: "3"

***

### shell?

> `optional` **shell?**: `boolean`

When true, command must be a single element, which is run using the container's shell

***

### startInterval?

> `optional` **startInterval?**: `string`

StartInterval configures the duration between checks during the startup phase. Example: "5s"

***

### startPeriod?

> `optional` **startPeriod?**: `string`

StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example: "0s"

***

### timeout?

> `optional` **timeout?**: `string`

Healthcheck timeout. Example: "3s"
