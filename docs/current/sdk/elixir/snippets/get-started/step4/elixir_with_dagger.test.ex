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

    # highlight-start
    [
      {"1.14.5", "25.3.2.5"},
      {"1.15.4", "25.3.2.5"}
    ]
    |> Task.async_stream(
      fn {elixir_version, erlang_version} ->
        # highlight-end
        elixir =
          client
          |> Dagger.Client.container()
          # highlight-start
          |> Dagger.Container.from(
            "hexpm/elixir:#{elixir_version}-erlang-#{erlang_version}-ubuntu-bionic-20230126"
          )
          # highlight-end
          |> Dagger.Container.with_mounted_directory("/a_project", project)
          |> Dagger.Container.with_workdir("/a_project")
          |> Dagger.Container.with_exec(~w"mix local.hex --force")
          |> Dagger.Container.with_exec(~w"mix local.rebar --force")
          |> Dagger.Container.with_exec(~w"mix deps.get")
          |> Dagger.Container.with_exec(~w"mix test")

        # highlight-start
        IO.puts("Starting tests for Elixir #{elixir_version} with Erlang OTP #{erlang_version}")
        {:ok, _} = Dagger.Sync.sync(elixir)
        IO.puts("Tests for Elixir #{elixir_version} with Erlang OTP #{erlang_version} succeeded!")
      end,
      timeout: :timer.minutes(10)
    )
    |> Stream.run()

    # highlight-end

    Dagger.close(client)

    # highlight-start
    IO.puts("All tasks have finished")
    # highlight-end
  end
end
