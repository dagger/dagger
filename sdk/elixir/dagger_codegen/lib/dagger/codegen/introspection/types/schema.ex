defmodule Dagger.Codegen.Introspection.Types.Schema do
  @derive [
    {Nestru.PreDecoder,
     translate: %{
       "mutationType" => :mutation_type,
       "queryType" => :query_type,
       "subscriptionType" => :subscription_type
     }},
    {Nestru.Decoder,
     hint: %{
       query_type: Dagger.Codegen.Introspection.Types.QueryType,
       types: [Dagger.Codegen.Introspection.Types.Type]
     }}
  ]
  defstruct [
    :mutation_type,
    :query_type,
    :subscription_type,
    :types
  ]

  def get_type(%__MODULE__{} = schema, type) do
    Enum.find(schema.types, &(&1.name == type.name))
  end
end
