defmodule Dagger.Codegen.Elixir.Templates.Scalar do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Module, as: Mod

  def render(%{"name" => name, "description" => desc}) do
    mod_name = name |> Mod.from_name()
    type = name_to_type(name)

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)
        @type t() :: unquote(type)
      end
    end
  end

  defp name_to_type("Int"), do: quote(do: integer())
  defp name_to_type("Float"), do: quote(do: float())
  defp name_to_type("Boolean"), do: quote(do: boolean())
  defp name_to_type("DateTime"), do: quote(do: DateTime.t())
  defp name_to_type(_), do: quote(do: String.t())
end
