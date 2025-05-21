defmodule ConstructorFunction do
  @moduledoc false

  use Dagger.Mod.Object, name: "ConstructorFunction"

  object do
    field :name, String.t()
  end

  defn init(name: String.t()) :: ConstructorFunction.t() do
    %__MODULE__{name: name}
  end

  defn greeting(self) :: String.t() do
    "Hello, #{self.name}!"
  end
end
