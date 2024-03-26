defmodule Dagger.Codegen.Introspection.Types.Type do
  @derive [
    {Nestru.PreDecoder,
     translate: %{
       "enumValues" => :enum_values,
       "inputFields" => :input_fields,
       "possibleTypes" => :possible_types
     }}
  ]
  defstruct [
    :description,
    :enum_values,
    :fields,
    :input_fields,
    :interfaces,
    :kind,
    :name,
    :possible_types
  ]

  defimpl Nestru.Decoder do
    def from_map_hint(_value, _context, map) do
      hint =
        %{}
        |> put_hint(
          map["inputFields"],
          :input_fields,
          [Dagger.Codegen.Introspection.Types.InputValue]
        )
        |> put_hint(
          map["enumValues"],
          :enum_values,
          [Dagger.Codegen.Introspection.Types.EnumValue]
        )
        |> put_hint(
          map["fields"],
          :fields,
          [Dagger.Codegen.Introspection.Types.Field]
        )

      {:ok, hint}
    end

    # Nestru cannot convert `null` value into a list. So we need to
    # add hint if the value is present

    defp put_hint(hint, value, _hint_key, _hint_fun) when is_nil(value) do
      hint
    end

    defp put_hint(hint, _value, hint_key, hint_fun) do
      Map.put(hint, hint_key, hint_fun)
    end
  end
end
