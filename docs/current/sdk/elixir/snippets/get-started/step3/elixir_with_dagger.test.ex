defmodule Mix.Tasks.ElixirWithDagger.Test do
  use Mix.Task

  @impl Mix.Task
  def run(_args) do
    Application.ensure_all_started(:dagger)

    client = Dagger.connect!()

    project =
      client
      |> Dagger.Client.host()
      |> Dagger.Host.directory(".", exclude: ["deps", "_build"])

    {:ok, _} =
      client
      |> Dagger.Client.container()
      |> Dagger.Container.from("hexpm/elixir:1.15.4-erlang-25.3.2.5-ubuntu-bionic-20230126")
      |> Dagger.Container.with_mounted_directory("/a_project", project)
      |> Dagger.Container.with_workdir("/a_project")
      |> Dagger.Container.with_exec(~w"mix local.hex --force")
      |> Dagger.Container.with_exec(~w"mix local.rebar --force")
      |> Dagger.Container.with_exec(~w"mix deps.get")
      |> Dagger.Container.with_exec(~w"mix test")
      |> Dagger.Sync.sync()

    Dagger.close(client)

    IO.puts("Tests succeeded!")
  end
end
