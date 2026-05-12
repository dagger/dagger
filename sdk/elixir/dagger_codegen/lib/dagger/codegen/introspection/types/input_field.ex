defmodule Dagger.Codegen.Introspection.Types.InputValue do
  defstruct [
    :default_value,
    :description,
    :name,
    :type
  ]

  def is_optional?(%__MODULE__{} = input_value) do
    input_value.type.kind != "NON_NULL"
  end

  def from_map(%{
        "defaultValue" => default_value,
        "description" => description,
        "name" => name,
        "type" => type
      }) do
    %__MODULE__{
      default_value: default_value,
      description: description,
      name: name,
      type: Dagger.Codegen.Introspection.Types.TypeRef.from_map(type)
    }
  end
end
