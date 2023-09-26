---
slug: /sdk/elixir/043817/install
---

# Installation

:::note
This Dagger Elixir SDK requires [Elixir 1.14 or later](https://elixir-lang.org/install.html) with [Erlang OTP 23 or later](https://www.erlang.org/downloads). Using Erlang OTP version 25 is recommended.
:::

:::warning
The Dagger Elixir SDK is currently experimental and is subject to change.
:::

Install the Dagger Elixir SDK in your project by adding the package in the `deps` function in `mix.exs` :

```elixir
def deps do
  [
    {:dagger, "~> 0.8"}
  ]
end
```
