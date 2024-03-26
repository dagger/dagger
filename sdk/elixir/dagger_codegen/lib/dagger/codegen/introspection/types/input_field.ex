defmodule Dagger.Codegen.Introspection.Types.InputValue do
  @derive [
    {Nestru.PreDecoder, translate: %{"defaultValue" => :default_value}},
    {Nestru.Decoder, hint: %{type: Dagger.Codegen.Introspection.Types.TypeRef}}
  ]
  defstruct [
    :default_value,
    :description,
    :name,
    :type
  ]

  def is_optional?(%__MODULE__{} = input_value) do
    input_value.type.kind != "NON_NULL"
  end
end
