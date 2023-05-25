# Dagger

[Dagger](dagger.io) SDK for Elixir.

**CAUTION**: It's not ready for production. The library is not complete an may changes during development. Please use with caution.

## Installation

Fetch from repository by:

```elixir
def deps do
  [
    {:dagger_ex, github: "wingyplus/dagger_ex"}
  ]
end
```

## Running

Let's write a code below into a script:

```elixir
client = Dagger.connect!()

client
|> Dagger.Query.container([])
|> Dagger.Container.from(address: "hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim")
|> Dagger.Container.with_exec(args: ["elixir", "--version"])
|> Dagger.Container.stdout()
|> IO.puts()
```

Then running with:

```shell
$ _EXPERIMENT_DAGGER_CLI_BIN=dagger elixir ci.exs
```

Where `ci.exs` contains Elixir script above.

## Supporting me

<a href="https://www.buymeacoffee.com/wingyplus" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" style="height: 60px !important;width: 217px !important;" ></a>
