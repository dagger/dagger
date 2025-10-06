defmodule Dagger.Codegen.Introspection.Types.Field do
  defstruct [
    :args,
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name,
    :type,
    :directives
  ]

  def no_args?(%__MODULE__{} = field) do
    field.args == []
  end

  def from_map(
        %{
          "args" => args,
          "deprecationReason" => deprecation_reason,
          "description" => description,
          "isDeprecated" => is_deprecated,
          "name" => name,
          "type" => type
        } = field
      ) do
    %__MODULE__{
      args: Enum.map(args, &Dagger.Codegen.Introspection.Types.InputValue.from_map/1),
      deprecation_reason:
        if not is_nil(deprecation_reason) do
          deprecation_reason
        end,
      description: description,
      is_deprecated: is_deprecated,
      name: name,
      type: Dagger.Codegen.Introspection.Types.TypeRef.from_map(type),
      directives:
        Enum.map(
          field["directives"] || [],
          &Dagger.Codegen.Introspection.Types.Directive.from_map/1
        )
    }
  end
end
