defmodule Dagger.Mod.Decoder do
  @moduledoc """
  Provides set of functions for decoding value from function call.
  """

  @doc """
  Decode the given `value` into a proper `type`.
  """
  def decode(value, type, dag)

  def decode(nil, type, dag) do
    cast(nil, type, dag)
  end

  def decode(value, type, dag) do
    with {:ok, value} <- Jason.decode(value) do
      cast(value, type, dag)
    end
  end

  defp cast(value, :integer, _) when is_integer(value) do
    {:ok, value}
  end

  defp cast(value, :float, _) when is_float(value) do
    {:ok, value}
  end

  defp cast(value, :boolean, _) when is_boolean(value) do
    {:ok, value}
  end

  defp cast(value, :string, _) when is_binary(value) do
    {:ok, value}
  end

  defp cast(values, {:list, type}, dag) when is_list(values) do
    values =
      for value <- values do
        {:ok, value} = cast(value, type, dag)
        value
      end

    {:ok, values}
  end

  defp cast(nil, {:optional, _type}, _dag), do: {:ok, nil}
  defp cast(value, {:optional, type}, dag), do: cast(value, type, dag)

  defp cast(value, module, dag) when is_atom(module) do
    Nestru.decode(value, module, dag)
  end

  defp cast(value, type, _) do
    {:error, "Cannot cast value #{value} to type #{type}."}
  end
end
