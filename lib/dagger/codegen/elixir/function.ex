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

  def define(fun_name, args, guard \\ nil, body) when is_atom(fun_name) and is_list(args) do
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
