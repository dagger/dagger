defmodule Dagger.Codegen.Elixir.Function do
  @moduledoc false

  def format_module_name(name) when is_binary(name) do
    name
    |> Macro.camelize()
    |> String.to_atom()
  end

  def format_name(name) when is_binary(name) do
    name
    |> Macro.underscore()
    |> String.to_atom()
  end

  def format_field_name(name) when is_atom(name) do
    name
    |> to_string()
    |> Recase.to_camel()
  end
end
