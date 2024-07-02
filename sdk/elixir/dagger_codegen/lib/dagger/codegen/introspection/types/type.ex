defmodule Dagger.Codegen.Introspection.Types.Type do
  defstruct [
    :description,
    :enum_values,
    :fields,
    :input_fields,
    :kind,
    :name
  ]

  def from_map(%{
        "description" => description,
        "enumValues" => enum_values,
        "fields" => fields,
        "inputFields" => input_fields,
        "kind" => kind,
        "name" => name
      }) do
    %__MODULE__{
      description: description,
      enum_values:
        Enum.map(enum_values, &Dagger.Codegen.Introspection.Types.EnumValue.from_map/1),
      fields: Enum.map(fields, &Dagger.Codegen.Introspection.Types.Field.from_map/1),
      input_fields:
        Enum.map(input_fields, &Dagger.Codegen.Introspection.Types.InputValue.from_map/1),
      kind: kind,
      name: name
    }
  end
end
