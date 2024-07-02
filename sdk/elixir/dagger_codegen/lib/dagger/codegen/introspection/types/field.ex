defmodule Dagger.Codegen.Introspection.Types.Field do
  defstruct [
    :args,
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name,
    :type
  ]

  def no_args?(%__MODULE__{} = field) do
    field.args == []
  end

  def from_map(%{
        "args" => args,
        "deprecationReason" => deprecation_reason,
        "description" => description,
        "isDeprecated" => is_deprecated,
        "name" => name,
        "type" => type
      }) do
    %__MODULE__{
      args: Enum.map(args, &Dagger.Codegen.Introspection.Types.InputValue.from_map/1),
      deprecation_reason: deprecation_reason,
      description: description,
      is_deprecated: is_deprecated,
      name: name,
      type: Dagger.Codegen.Introspection.Types.TypeRef.from_map(type)
    }
  end
end
