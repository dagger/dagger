---
slug: /sdk/elixir/043817/install
---

# Installation

:::note
This Dagger Elixir SDK requires Elixir 1.14 or later with Erlang OTP 23 or later. Using Erlang OTP version 25 is recommended.
:::

Install the Dagger Elixir SDK in your project by adding this package into `mix.exs` in `deps` function:

```elixir
def deps do
  [
    {:dagger, "~> 0.8"}
  ]
end
```
