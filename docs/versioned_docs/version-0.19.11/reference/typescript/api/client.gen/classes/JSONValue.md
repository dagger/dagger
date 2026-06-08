[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / JSONValue

# Class: JSONValue

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new JSONValue**(`ctx?`, `_id?`, `_asBoolean?`, `_asInteger?`, `_asString?`, `_contents?`): `JSONValue`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`JSONValueID`](../type-aliases/JSONValueID.md)

##### \_asBoolean?

`boolean`

##### \_asInteger?

`number`

##### \_asString?

`string`

##### \_contents?

[`JSON`](../type-aliases/JSON.md)

#### Returns

`JSONValue`

#### Overrides

`BaseClient.constructor`

## Methods

### asArray()

> **asArray**(): `Promise`\<`JSONValue`[]\>

Decode an array from json

#### Returns

`Promise`\<`JSONValue`[]\>

***

### asBoolean()

> **asBoolean**(): `Promise`\<`boolean`\>

Decode a boolean from json

#### Returns

`Promise`\<`boolean`\>

***

### asInteger()

> **asInteger**(): `Promise`\<`number`\>

Decode an integer from json

#### Returns

`Promise`\<`number`\>

***

### asString()

> **asString**(): `Promise`\<`string`\>

Decode a string from json

#### Returns

`Promise`\<`string`\>

***

### contents()

> **contents**(`opts?`): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

Return the value encoded as json

#### Parameters

##### opts?

[`JSONValueContentsOpts`](../type-aliases/JSONValueContentsOpts.md)

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>

***

### field()

> **field**(`path`): `JSONValue`

Lookup the field at the given path, and return its value.

#### Parameters

##### path

`string`[]

Path of the field to lookup, encoded as an array of field names

#### Returns

`JSONValue`

***

### fields()

> **fields**(): `Promise`\<`string`[]\>

List fields of the encoded object

#### Returns

`Promise`\<`string`[]\>

***

### id()

> **id**(): `Promise`\<[`JSONValueID`](../type-aliases/JSONValueID.md)\>

A unique identifier for this JSONValue.

#### Returns

`Promise`\<[`JSONValueID`](../type-aliases/JSONValueID.md)\>

***

### newBoolean()

> **newBoolean**(`value`): `JSONValue`

Encode a boolean to json

#### Parameters

##### value

`boolean`

New boolean value

#### Returns

`JSONValue`

***

### newInteger()

> **newInteger**(`value`): `JSONValue`

Encode an integer to json

#### Parameters

##### value

`number`

New integer value

#### Returns

`JSONValue`

***

### newString()

> **newString**(`value`): `JSONValue`

Encode a string to json

#### Parameters

##### value

`string`

New string value

#### Returns

`JSONValue`

***

### with()

> **with**(`arg`): `JSONValue`

Call the provided function with current JSONValue.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `JSONValue`

#### Returns

`JSONValue`

***

### withContents()

> **withContents**(`contents`): `JSONValue`

Return a new json value, decoded from the given content

#### Parameters

##### contents

[`JSON`](../type-aliases/JSON.md)

New JSON-encoded contents

#### Returns

`JSONValue`

***

### withField()

> **withField**(`path`, `value`): `JSONValue`

Set a new field at the given path

#### Parameters

##### path

`string`[]

Path of the field to set, encoded as an array of field names

##### value

`JSONValue`

The new value of the field

#### Returns

`JSONValue`
