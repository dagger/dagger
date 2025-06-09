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

    quote do
      use Dagger.Core.Base, kind: :enum, name: unquote(name)

      def __enum__(:keys), do: unquote(values)

      unquote(functions)
    end
  end

  defp defenum(key) when is_atom(key) do
    value = Atom.to_string(key)

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: nil
    end
  end

  defp defenum({key, options}) when is_atom(key) and is_list(options) do
    value = Atom.to_string(key)
    doc = options[:doc]

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: unquote(doc)
    end
  end

  defp defenum({key, value}) when is_atom(key) and is_binary(value) do
    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: nil
    end
  end

  defp defenum({key, {value, options}})
       when is_atom(key) and is_binary(value) and is_list(options) do
    doc = options[:doc]

    quote do
      def __enum__(:value, unquote(key)), do: unquote(value)
      def __enum__(:key, unquote(value)), do: unquote(key)
      def __enum__(:doc, unquote(key)), do: unquote(doc)
    end
  end
end
