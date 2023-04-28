defmodule Mix.Tasks.Ci.Test do
  @moduledoc "Test the project with Dagger."

  use Mix.Task

  def run(_) do
    Application.ensure_all_started(:dagger_ex)

    elixir_image = "hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim"

    client = Dagger.connect!()

    repo =
      client
      |> Dagger.Query.host()
      |> Dagger.Host.directory(".", exclude: [".elixir_ls", "_build", "deps"])
      |> Dagger.Directory.id()

    client
    |> Dagger.Query.pipeline("Test")
    |> Dagger.Query.container()
    |> Dagger.Container.from(elixir_image)
    |> Dagger.Container.with_mounted_directory("/dagger_ex", repo)
    |> Dagger.Container.with_workdir("/dagger_ex")
    |> Dagger.Container.with_env_variable("MIX_ENV", "test")
    |> Dagger.Container.with_exec(["mix", "local.rebar", "--force"])
    |> Dagger.Container.with_exec(["mix", "local.hex", "--force"])
    |> Dagger.Container.with_exec(["mix", "deps.get"])
    |> Dagger.Container.with_exec(["mix", "test", "--color"])
    |> Dagger.Container.stdout()
    |> IO.puts()

    Dagger.disconnect(client)
  end
end
