defmodule Dagger.Codegen.Introspection.Types.Schema do
  defstruct [
    :version,
    :query_type,
    :types
  ]

  def get_type(%__MODULE__{} = schema, type) do
    Enum.find(schema.types, &(&1.name == type.name))
  end

  @doc """
  Convert a schema map from introspection.json into module.
  """
  def from_map(%{"__schema" => schema} = response) do
    schema
    |> Map.put("__schemaVersion", response["__schemaVersion"])
    |> from_map()
  end

  def from_map(%{"queryType" => query_type, "types" => types} = schema) do
    %__MODULE__{
      version: schema["__schemaVersion"],
      query_type: Dagger.Codegen.Introspection.Types.QueryType.from_map(query_type),
      types: Enum.map(types, &Dagger.Codegen.Introspection.Types.Type.from_map/1)
    }
  end
end
