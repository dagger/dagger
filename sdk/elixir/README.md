> **Warning** This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

# Dagger

[Dagger](https://dagger.io) SDK for Elixir.

## Installation

Fetch from repository by:

```elixir
def deps do
  [
    {:dagger, github: "dagger/dagger", sparse: "sdk/elixir"}
  ]
end
```

## Running

Let's write a code below into a script:

```elixir
# ci.exs
client = Dagger.connect!()

{:ok, out} =
  client
  |> Dagger.Client.container([])
  |> Dagger.Container.from("hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim")
  |> Dagger.Container.with_exec(["elixir", "--version"])
  |> Dagger.Container.stdout()

IO.puts(out)

Dagger.close(client)
```

Then running with:

```shell
elixir ci.exs
```

Where `ci.exs` contains Elixir script above.

## Using with Dagger Function

Install the Elixir SDK module and initialize a module with:

```shell
dagger module install github.com/dagger/elixir-sdk
dagger module init elixir-sdk --name <name>
```

**CAUTIONS**: Please note that `dagger` version 0.11.6 and earlier are not
compatible with the runtime on `main` branch. If you are using `dagger` v0.11.6, please pin the sdk to `github.com/dagger/dagger/sdk/elixir/runtime@sdk/elixir/v0.11.6`
instead.

The SDK will generate 2 modules inside the `dagger` directory (or the destination defined
by `--path` during `dagger module init <SDK>`):

1. The `dagger` SDK itself.
2. The package `<name>` that contains your functions.
