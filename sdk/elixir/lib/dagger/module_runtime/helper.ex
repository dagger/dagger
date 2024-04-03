defmodule Dagger.ModuleRuntime.Helper do
  @moduledoc false

  @doc """
  Convert the `name` into string camel case.
  """
  def camelize(name) do
    name |> to_string() |> Macro.camelize()
  end
end
