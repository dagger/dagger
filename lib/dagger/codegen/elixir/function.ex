defmodule Dagger.Codegen.Elixir.Function do
  @moduledoc false

  def format_module_name(name) when is_binary(name) do
    name
    |> Macro.camelize()
    |> String.to_atom()
  end

  def format_var_name(name) when is_binary(name) do
    format_name(name)
  end

  defp format_fun_name(name) when is_atom(name) do
    name
    |> to_string()
    |> format_fun_name()
  end

  defp format_fun_name(name) when is_binary(name) do
    format_name(name)
  end

  defp format_name(name) when is_binary(name) do
    name
    |> Macro.underscore()
    |> String.to_atom()
  end

  def define(fun_name, args, guard \\ nil, body) when is_list(args) do
    fun_name = format_fun_name(fun_name)

    case guard do
      nil ->
        quote do
          def unquote(fun_name)(unquote_splicing(args)) do
            unquote(body)
          end
        end

      guard ->
        quote do
          def unquote(fun_name)(unquote_splicing(args)) when unquote(guard) do
            unquote(body)
          end
        end
    end
  end
end
