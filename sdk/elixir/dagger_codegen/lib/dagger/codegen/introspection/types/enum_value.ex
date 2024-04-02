defmodule Dagger.Codegen.Introspection.Types.EnumValue do
  @derive [
    {Nestru.PreDecoder,
     translate: %{"deprecationReason" => :deprecation_reason, "isDeprecated" => :is_deprecated}},
    Nestru.Decoder
  ]
  defstruct [
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name
  ]
end
