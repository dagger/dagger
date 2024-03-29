defmodule Dagger.Codegen.Introspection.Types.Field do
  @derive [
    {Nestru.PreDecoder,
     translate: %{"deprecationReason" => :deprecation_reason, "isDeprecated" => :is_deprecated}},
    {Nestru.Decoder,
     hint: %{
       type: Dagger.Codegen.Introspection.Types.TypeRef,
       args: [Dagger.Codegen.Introspection.Types.InputValue]
     }}
  ]
  defstruct [
    :args,
    :deprecation,
    :deprecation_reason,
    :description,
    :is_deprecated,
    :name,
    :type
  ]

  def no_args?(%__MODULE__{} = field) do
    field.args == []
  end
end
