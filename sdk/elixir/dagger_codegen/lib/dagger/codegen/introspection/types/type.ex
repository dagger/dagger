defmodule Dagger.Codegen.Introspection.Types.Type do
  defstruct [
    :description,
    :enum_values,
    :fields,
    :input_fields,
    :kind,
    :name
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
      kind: kind,
      name: name
    }
  end
end
