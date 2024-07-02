defmodule Dagger.Codegen.Introspection.Types.QueryType do
  defstruct [:name]

  def from_map(query_type) do
    %__MODULE__{
      name: Map.fetch!(query_type, "name")
    }
  end
end
