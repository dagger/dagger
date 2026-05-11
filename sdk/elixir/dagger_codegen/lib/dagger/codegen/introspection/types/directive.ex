defmodule Dagger.Codegen.Introspection.Types.Directive.Arg do
  defstruct [:name, :value]

  def from_map(%{"name" => name, "value" => value}) do
    %__MODULE__{name: name, value: value}
  end
end

defmodule Dagger.Codegen.Introspection.Types.Directive do
  defstruct [:name, :args]

  def from_map(%{"name" => name, "args" => args}) do
    %__MODULE__{name: name, args: Enum.map(args, &__MODULE__.Arg.from_map/1)}
  end

  @doc """
  Extract the expected type name from an `@expectedType` directive.

  Returns `nil` if no `@expectedType` directive is found.
  """
  def expected_type(directives) when is_list(directives) do
    Enum.find_value(directives, fn
      %__MODULE__{name: "expectedType", args: args} ->
        Enum.find_value(args, fn
          %__MODULE__.Arg{name: "name", value: value} ->
            value |> String.trim_leading("\"") |> String.trim_trailing("\"")

          _ ->
            nil
        end)

      _ ->
        nil
    end)
  end

  def expected_type(_), do: nil
end
