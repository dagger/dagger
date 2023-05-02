defmodule Dagger.Codegen.Elixir.Templates.Enum do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function
  alias Dagger.Codegen.Elixir.Module, as: Mod

  def render(%{"name" => name, "description" => desc, "enumValues" => enum_values}) do
    mod_name = Module.concat([Dagger, Mod.format_name(name)])

    type = render_possible_enum_values(enum_values)

    funs =
      for %{
            "name" => value,
            "description" => desc,
            "deprecationReason" => deprecated_reason
          } <-
            enum_values do
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

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: unquote(type)

        unquote_splicing(funs)
      end
    end
  end

  defp render_possible_enum_values([%{"name" => v1}, %{"name" => v2}]) do
    {:|, [], [String.to_atom(v1), String.to_atom(v2)]}
  end

  defp render_possible_enum_values([%{"name" => v1} | rest]) do
    {:|, [], [String.to_atom(v1), render_possible_enum_values(rest)]}
  end
end
