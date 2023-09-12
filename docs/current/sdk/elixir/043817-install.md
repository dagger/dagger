---
slug: /sdk/elixir/043817/install
---

# Installation

:::note
The Dagger Elixir SDK requires Elixir 1.14 or later with Erlang OTP 23 or later. Using Erlang OTP 25 is recommended.
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
