:::note
This Dagger Elixir SDK requires [Elixir 1.14 or later](https://elixir-lang.org/install.html) with [Erlang OTP 23 or later](https://www.erlang.org/downloads). Using Erlang OTP version 25 is recommended.
:::

In your project directory, open `mix.exs` and add `{:dagger, "~> 0.8"}` to the list in the `deps` function:

```elixir
def deps do
  [
    {:dagger, "~> 0.8", only: [:dev, :test]}
  ]
end
```

Run `mix deps.get` to fetch the Elixir SDK from Hex.pm:

```shell
mix deps.get
```
