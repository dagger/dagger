defmodule Dagger.Codegen.Introspection.Types.EnumValue do
  defstruct [
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name
  ]

  def from_map(%{
        "deprecationReason" => deprecation_reason,
        "description" => description,
        "isDeprecated" => is_deprecated,
        "name" => name
      }) do
    %__MODULE__{
      deprecation_reason: deprecation_reason,
      description: description,
      is_deprecated: is_deprecated,
      name: name
    }
  end
end
