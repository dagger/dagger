---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: HealthcheckConfig

Image healthcheck configuration.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new HealthcheckConfig**(`ctx?`, `_id?`, `_interval?`, `_retries?`, `_shell?`, `_startInterval?`, `_startPeriod?`, `_timeout?`): `HealthcheckConfig`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_interval?

`string`

##### \_retries?

`number`

##### \_shell?

`boolean`

##### \_startInterval?

`string`

##### \_startPeriod?

`string`

##### \_timeout?

`string`

#### Returns

`HealthcheckConfig`

#### Overrides

`BaseClient.constructor`

## Methods

### args()

> **args**(): `Promise`\<`string`[]\>

Healthcheck command arguments.

#### Returns

`Promise`\<`string`[]\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this HealthcheckConfig.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### interval()

> **interval**(): `Promise`\<`string`\>

Interval between running healthcheck. Example:30s

#### Returns

`Promise`\<`string`\>

***

### retries()

> **retries**(): `Promise`\<`number`\>

The maximum number of consecutive failures before the container is marked as unhealthy. Example:3

#### Returns

`Promise`\<`number`\>

***

### shell()

> **shell**(): `Promise`\<`boolean`\>

Healthcheck command is a shell command.

#### Returns

`Promise`\<`boolean`\>

***

### startInterval()

> **startInterval**(): `Promise`\<`string`\>

StartInterval configures the duration between checks during the startup phase. Example:5s

#### Returns

`Promise`\<`string`\>

***

### startPeriod()

> **startPeriod**(): `Promise`\<`string`\>

StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example:0s

#### Returns

`Promise`\<`string`\>

***

### timeout()

> **timeout**(): `Promise`\<`string`\>

Healthcheck timeout. Example:3s

#### Returns

`Promise`\<`string`\>
