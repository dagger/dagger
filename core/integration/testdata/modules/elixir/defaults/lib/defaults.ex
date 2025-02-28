defmodule Defaults do
  @moduledoc false

  use Dagger.Mod.Object, name: "Defaults"

  defn echo_else(value: String.t() | nil) :: String.t() do
    if(value, do: value, else: "default value if null")
  end
end
