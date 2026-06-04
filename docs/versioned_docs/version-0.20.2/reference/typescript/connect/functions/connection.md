[**@dagger.io/dagger**](../../README.md)

***

[@dagger.io/dagger](../../modules.md) / [connect](../README.md) / connection

# Function: connection()

> **connection**(`fct`, `cfg?`): `Promise`\<`void`\>

connection executes the given function using the default global Dagger client.

## Parameters

### fct

() => `Promise`\<`void`\>

### cfg?

`ConnectOpts` = `{}`

## Returns

`Promise`\<`void`\>

## Example

```ts
await connection(
  async () => {
    await dag
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])
      .withExec(["curl", "https://dagger.io/"])
      .sync()
  }, { LogOutput: process.stderr }
)
```
