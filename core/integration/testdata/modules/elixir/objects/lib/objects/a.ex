defmodule Objects.A do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectsA"

  object do
  end

  defn message() :: String.t() do
    "Hello from A"
  end

  defn object_b() :: Objects.B.t() do
    %Objects.B{}
  end
end
