[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / CacheVolume

# Class: CacheVolume

A directory whose contents persist across runs.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new CacheVolume**(`ctx?`, `_id?`): `CacheVolume`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`CacheVolumeID`](../type-aliases/CacheVolumeID.md)

#### Returns

`CacheVolume`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`CacheVolumeID`](../type-aliases/CacheVolumeID.md)\>

A unique identifier for this CacheVolume.

#### Returns

`Promise`\<[`CacheVolumeID`](../type-aliases/CacheVolumeID.md)\>
