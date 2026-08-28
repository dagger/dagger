[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerWithEnvVariableOpts

# Type Alias: ContainerWithEnvVariableOpts

> **ContainerWithEnvVariableOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value according to the current environment variables defined in the container (e.g. "/opt/bin:$PATH").
