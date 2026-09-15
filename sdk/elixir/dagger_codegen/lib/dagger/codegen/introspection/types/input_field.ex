defmodule Dagger.Codegen.Introspection.Types.InputValue do
  defstruct [
    :default_value,
    :description,
    :name,
    :type,
    :directives
  ]

  def is_optional?(%__MODULE__{} = input_value) do
    input_value.type.kind != "NON_NULL"
  end

  def from_map(%{
        "defaultValue" => default_value,
        "description" => description,
        "name" => name,
        "type" => type
      } = input_value) do
    %__MODULE__{
      default_value: default_value,
      description: description,
      name: name,
      type: Dagger.Codegen.Introspection.Types.TypeRef.from_map(type),
      directives:
        Enum.map(
          input_value["directives"] || [],
          &Dagger.Codegen.Introspection.Types.Directive.from_map/1
        )
    }
  end
end
