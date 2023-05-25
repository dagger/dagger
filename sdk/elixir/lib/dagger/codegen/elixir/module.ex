defmodule Dagger.Codegen.Elixir.Module do
  @moduledoc false

  def format_name(name) when is_binary(name) do
    name
    |> Macro.camelize()
    |> String.to_atom()
  end
end
