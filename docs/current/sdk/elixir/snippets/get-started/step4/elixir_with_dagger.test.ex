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

    # highlight-start
    [
      {"1.14.4", "erlang-25.3.2", "alpine-3.18.0"},
      {"1.15.0-rc.2", "erlang-26.0.1", "alpine-3.18.2"}
    ]
    |> Task.async_stream(
      fn {elixir_version, erlang_version, os_version} ->
        elixir =
        # highlight-end
          client
          |> Dagger.Client.container()
          # highlight-start
          |> Dagger.Container.from(
            "hexpm/elixir:#{elixir_version}-#{erlang_version}-#{os_version}"
          )
          # highlight-end
          |> Dagger.Container.with_mounted_directory("/app", app)
          |> Dagger.Container.with_workdir("/app")
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
