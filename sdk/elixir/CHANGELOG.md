# Changelog

## 0.2.0

### Changes

* Compile enum and scalar types into Elixir module.
* Every functions are now have a typespec. Please note that some functions that return a map, such as, `Dagger.Container.env_variables/1` 
  still return a plain map not struct.

### Breaking Changes!

Any function that accept id are now accept a struct instead of id. That function will get id for you. For example in our CI test:

Before:

```elixir
repo =
  client
  |> Dagger.Query.host()
  |> Dagger.Host.directory(".", exclude: [".elixir_ls", "_build", "deps"])
  |> Dagger.Directory.id()

base_image =
  client
  |> Dagger.Query.pipeline("Prepare")
  |> Dagger.Query.container()
  |> Dagger.Container.from(elixir_image)
  |> Dagger.Container.with_mounted_directory("/dagger", repo)
```

After:

```elixir
repo =
  client
  |> Dagger.Query.host()
  |> Dagger.Host.directory(".", exclude: [".elixir_ls", "_build", "deps"])

base_image =
  client
  |> Dagger.Query.pipeline("Prepare")
  |> Dagger.Query.container()
  |> Dagger.Container.from(elixir_image)
  |> Dagger.Container.with_mounted_directory("/dagger", repo)
```

## 0.1.1

* Support Dagger v0.5.1
* Fix source reference not presents in ExDoc.

## 0.1.0

Kick-off the project <3

This version start rollout support for API defined in [Dagger Reference](https://docs.dagger.io/api/reference/)
documentation. The type will represents as a module under `Dagger` namespace and field as a function.

Please note that this version is not stable API, the API may change version to version.

