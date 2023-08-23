defmodule Mix.Tasks.AProject.Test do
  use Mix.Task

  @impl Mix.Task
  def run(_args) do
    Application.ensure_all_started(:dagger)

    client = Dagger.connect!()

    project =
      client
      |> Dagger.Client.host()
      |> Dagger.Host.directory(".", exclude: ["deps", "_build"])

    # highlight-start
    [
      {"1.14.5", "25.3.2.5"},
      {"1.15.4", "25.3.2.5"},
      {"1.15.4", "26.0.2"}
    ]
    |> Task.async_stream(fn {elixir, erlang} ->
    # highlight-end
      {:ok, _} =
        client
        |> Dagger.Client.container()
        # highlight-start
        |> Dagger.Container.from("hexpm/elixir:#{elixir}-erlang-#{erlang}-ubuntu-bionic-20230126")
        # highlight-end
        |> Dagger.Container.with_mounted_directory("/a_project", project)
        |> Dagger.Container.with_workdir("/a_project")
        |> Dagger.Container.with_exec(~w"mix local.hex --force")
        |> Dagger.Container.with_exec(~w"mix local.rebar --force")
        |> Dagger.Container.with_exec(~w"mix deps.get")
        |> Dagger.Container.with_exec(~w"mix test")
        |> Dagger.Sync.sync()
    # highlight-start
    end, timeout: :infinity)
    |> Enum.to_list()
    # highlight-end

    Dagger.close(client)
  end
end
