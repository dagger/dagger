defmodule Mix.Tasks.ElixirWithDagger.Test do
  use Mix.Task

  @impl Mix.Task
  def run(_args) do
    Application.ensure_all_started(:dagger)

    client = Dagger.connect!()

    app =
      client
      |> Dagger.Client.host()
      |> Dagger.Host.directory(".", exclude: ["deps", "_build"])

    {:ok, _} =
      client
      |> Dagger.Client.container()
      |> Dagger.Container.from("hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0")
      |> Dagger.Container.with_mounted_directory("/app", app)
      |> Dagger.Container.with_workdir("/app")
      |> Dagger.Container.with_exec(~w"mix local.hex --force")
      |> Dagger.Container.with_exec(~w"mix local.rebar --force")
      |> Dagger.Container.with_exec(~w"mix deps.get")
      |> Dagger.Container.with_exec(~w"mix test")
      |> Dagger.Sync.sync()

    Dagger.close(client)

    IO.puts("Tests succeeded!")
  end
end
