defmodule Objects do
  @moduledoc false

  use Dagger.Mod.Object, name: "Objects"

  object do
    field :foo, String.t()
  end

  defn init(foo: {String.t(), default: "bar"}) :: Objects.t() do
    %__MODULE__{foo: foo}
  end

  defn object_a() :: Objects.A.t() do
    %Objects.A{}
  end
end
