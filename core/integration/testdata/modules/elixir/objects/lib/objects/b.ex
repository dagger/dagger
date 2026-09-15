defmodule Objects.B do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectsB"

  object do
  end

  defn message() :: String.t() do
    "Hello from B"
  end
end
