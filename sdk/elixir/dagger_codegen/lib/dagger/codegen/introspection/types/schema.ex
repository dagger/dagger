defmodule Dagger.Codegen.Introspection.Types.Schema do
  defstruct [
    :version,
    :query_type,
    :types,
    # The schema's names as the engine formatted them (see
    # `Dagger.Codegen.Naming`), or nil when there are none.
    :names
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

  @doc """
  Use `names`, the decoded names file (see `Dagger.Codegen.Naming.from_map/1`),
  for the schema's names. They are only used for a schema with
  `Query.formatIdentifiers`, the gate for formatting names through the engine:
  older schemas keep the legacy conversion.
  """
  def put_names(%__MODULE__{} = schema, names) do
    if format_identifiers?(schema) do
      %{schema | names: names}
    else
      %{schema | names: nil}
    end
  end

  @doc "Whether the schema has `Query.formatIdentifiers`."
  def format_identifiers?(%__MODULE__{query_type: query_type, types: types}) do
    query_name = (query_type && query_type.name) || "Query"

    Enum.any?(types, fn type ->
      type.name == query_name and Enum.any?(type.fields || [], &(&1.name == "formatIdentifiers"))
    end)
  end
end
