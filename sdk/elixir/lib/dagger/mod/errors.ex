defmodule Dagger.Mod.TypeMismatchError do
  @moduledoc """
  An error raise when the value is incompatible with type.
  """

  defexception [:value, :type]

  @impl true
  def message(exception) do
    "Expected value `#{inspect(exception.value)}` to be type `#{inspect(exception.type)}`."
  end
end
