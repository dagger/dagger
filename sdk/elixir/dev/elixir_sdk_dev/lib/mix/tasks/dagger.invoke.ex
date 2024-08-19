defmodule Mix.Tasks.Dagger.Invoke do
  use Mix.Task

  def run(_args) do
    Application.ensure_all_started(:dagger)
    Dagger.Mod.invoke(ElixirSdkDev)
  end
end
