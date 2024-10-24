defmodule Mix.Tasks.Dagger.Entrypoint.Invoke do
  @shortdoc "Main entrypoint for invoking a Dagger Module."

  @moduledoc """
  Main entrypoint for invoking a Dagger Module.

  NOTE: This task can run only inside Dagger Elixir runtime.

  ## Arguments

  - `module` - A main module to invoke. (e.g. `Potato`)
  """

  use Mix.Task

  def run([module]) do
    Mix.Task.run("compile")
    Mix.Task.reenable("dagger.entrypoint.invoke")

    module =
      module
      |> String.split(".")
      |> Module.concat()

    Code.ensure_loaded?(module) ||
      Mix.raise("Cannot find module #{module}")

    Dagger.Mod.invoke(module)
  end
end
