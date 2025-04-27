defmodule Dagger.Codegen.Introspection.Types.EnumValue do
  defstruct [
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name,
    :directives
  ]

  def from_map(
        %{
          "deprecationReason" => deprecation_reason,
          "description" => description,
          "isDeprecated" => is_deprecated,
          "name" => name
        } = enum_value
      ) do
    %__MODULE__{
      deprecation_reason: deprecation_reason,
      description: description,
      is_deprecated: is_deprecated,
      name: name,
      directives:
        Enum.map(
          enum_value["directives"] || [],
          &Dagger.Codegen.Introspection.Types.Directive.from_map/1
        )
    }
  end
end
