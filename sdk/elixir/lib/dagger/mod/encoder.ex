defmodule Dagger.Mod.Encoder do
  @moduledoc """
  Provides set of functions for encoding value from function call.
  """

  @doc """
  Validate the given `value` and encoding it.
  """
  def validate_and_encode(value, type) do
    with {:ok, value} <- validate(value, type) do
      encode(value)
    end
  end

  defp validate(value, :integer) when is_integer(value) do
    {:ok, value}
  end

  defp validate(value, :float) when is_float(value) do
    {:ok, value}
  end

  defp validate(value, :boolean) when is_boolean(value) do
    {:ok, value}
  end

  defp validate(value, :string) when is_binary(value) do
    {:ok, value}
  end

  defp validate(values, {:list, type}) when is_list(values) do
    Enum.reduce_while(values, {:ok, []}, fn value, {:ok, acc} ->
      case validate(value, type) do
        {:ok, value} -> {:cont, {:ok, [value | acc]}}
        error -> {:halt, error}
      end
    end)
    |> case do
      {:ok, values} -> {:ok, Enum.reverse(values)}
      error -> error
    end
  end

  defp validate(_value, Dagger.Void) do
    {:ok, nil}
  end

  defp validate(%module{} = value, module) do
    {:ok, value}
  end

  defp validate(value, type) do
    {:error, %Dagger.Mod.TypeMismatchError{value: value, type: type}}
  end

  defp encode(value) do
    Jason.encode(value)
  end
end
