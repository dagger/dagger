defmodule Dagger.Codegen.Introspection.Types.Type do
  defstruct [
    :description,
    :enum_values,
    :fields,
    :input_fields,
    :interfaces,
    :kind,
    :name,
    :possible_types
  ]

  def from_map(
        %{
          "kind" => kind,
          "name" => name
        } = type
      ) do
    %__MODULE__{
      description: type["description"],
      enum_values:
        Enum.map(
          type["enumValues"] || [],
          &Dagger.Codegen.Introspection.Types.EnumValue.from_map/1
        ),
      fields:
        Enum.map(type["fields"] || [], &Dagger.Codegen.Introspection.Types.Field.from_map/1),
      input_fields:
        Enum.map(
          type["inputFields"] || [],
          &Dagger.Codegen.Introspection.Types.InputValue.from_map/1
        ),
      interfaces:
        Enum.map(
          type["interfaces"] || [],
          &Dagger.Codegen.Introspection.Types.TypeRef.from_map/1
        ),
      kind: kind,
      name: name,
      possible_types:
        Enum.map(
          type["possibleTypes"] || [],
          &Dagger.Codegen.Introspection.Types.TypeRef.from_map/1
        )
    }
  end
end
