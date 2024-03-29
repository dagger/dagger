defmodule Dagger.ModuleRuntime.Helper do
  @doc """
  """
  def camelize(name) do
    name |> to_string() |> Macro.camelize()
  end
end
