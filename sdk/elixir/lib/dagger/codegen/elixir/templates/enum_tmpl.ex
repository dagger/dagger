defmodule Dagger.Codegen.Elixir.Templates.Enum do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function

  def render(%{
        "description" => desc,
        "enumValues" => enum_values,
        "private" => %{mod_name: mod_name}
      }) do
    type = render_possible_enum_values(enum_values)

    funs =
      enum_values
      |> Enum.sort_by(fn %{"name" => name} -> name end)
      |> Enum.map(&render_function/1)

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: unquote(type)

        unquote_splicing(funs)
      end
    end
  end

  defp render_function(%{
         "name" => value,
         "description" => desc,
         "deprecationReason" => deprecated_reason
       }) do
    Function.define(
      value,
      [],
      nil,
      quote do
        unquote(String.to_atom(value))
      end,
      doc: desc,
      deprecated: deprecated_reason,
      spec: {[], quote(do: unquote(String.to_atom(value)))}
    )
  end

  defp render_possible_enum_values([%{"name" => v1}, %{"name" => v2}]) do
    {:|, [], [String.to_atom(v1), String.to_atom(v2)]}
  end

  defp render_possible_enum_values([%{"name" => v1} | rest]) do
    {:|, [], [String.to_atom(v1), render_possible_enum_values(rest)]}
  end
end
