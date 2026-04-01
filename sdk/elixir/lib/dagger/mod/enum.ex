defmodule Dagger.Mod.Enum do
  @moduledoc """
  Declare a module as an enum type.
  """

  defmacro __using__(opts) do
    values = opts[:values]
    name = opts[:name]

    if is_nil(values) do
      raise "The option `:values` need to be set."
    end

    functions = Enum.map(values, &defenum/1)

    atoms =
      Enum.map_join(values, "|", fn v ->
        case v do
          {k, _v} when is_atom(k) -> k
          k when is_atom(k) -> k
        end
        |> Macro.to_string()
      end)

    {:ok, ast_type} = Code.string_to_quoted("@type t() :: #{atoms}")

    quote do
      use Dagger.Core.Base, kind: :enum, name: unquote(name)

      unquote(ast_type)

      def __enum__(:name), do: unquote(name)
      def __enum__(:keys), do: unquote(values)

      unquote_splicing(functions)
    end
  end

  defp defenum(key) when is_atom(key) do
    value = Atom.to_string(key)
    fname = String.downcase(value) |> String.to_atom()

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: nil

      def unquote(fname)(), do: unquote(key)
      def from_string(unquote(value)), do: unquote(key)
    end
  end

  defp defenum({key, options}) when is_atom(key) and is_list(options) do
    value = Atom.to_string(key)
    doc = options[:doc]
    fname = String.downcase(value) |> String.to_atom()

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: unquote(doc)

      def unquote(fname)(), do: unquote(key)
      def from_string(unquote(value)), do: unquote(key)
    end
  end

  defp defenum({key, value}) when is_atom(key) and is_binary(value) do
    fname = String.downcase(value) |> String.to_atom()

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: nil

      def unquote(fname)(), do: unquote(key)
      def from_string(unquote(value)), do: unquote(key)
    end
  end

  defp defenum({key, {value, options}})
       when is_atom(key) and is_binary(value) and is_list(options) do
    doc = options[:doc]
    fname = String.downcase(value) |> String.to_atom()

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: unquote(doc)

      def unquote(fname)(), do: unquote(key)
      def from_string(unquote(value)), do: unquote(key)
    end
  end

  def get_key_description(module, key) do
    if Code.ensure_loaded?(module) and function_exported?(module, :__enum__, 2) do
      module.__enum__(:doc, key)
    else
      nil
    end
  end
end
