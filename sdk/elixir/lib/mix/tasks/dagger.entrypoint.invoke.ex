defmodule Mix.Tasks.Dagger.Entrypoint.Invoke do
  @shortdoc "Main entrypoint for invoking a Dagger Module."

  @moduledoc """
  Main entrypoint for invoking a Dagger Module.

  NOTE: This task can run only inside Dagger Elixir runtime.

  ## Arguments

  - `module` - A main module to invoke. (e.g. `Potato`)
  """

  use Mix.Task

  def run(["register", module]) do
    Application.ensure_all_started(:dagger)

    Mix.Task.run("compile")
    Mix.Task.reenable("dagger.entrypoint.invoke")

    module = load_module(module)
    Dagger.Mod.register(module)
  end

  def run([module]) do
    Application.ensure_all_started(:dagger)

    Mix.Task.run("compile")
    Mix.Task.reenable("dagger.entrypoint.invoke")

    module = load_module(module)
    Dagger.Mod.invoke(module)
  end

  defp load_module(module) do
    module =
      module
      |> String.split(".")
      |> Module.concat()

    Code.ensure_loaded?(module) ||
      Mix.raise("Cannot find module #{module}")

    module
  end
end
