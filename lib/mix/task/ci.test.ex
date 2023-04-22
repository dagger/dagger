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
      |> Dagger.Host.directory(path: ".", exclude: [".elixir_ls", "_build", "deps"])
      |> Dagger.Directory.id()

    client
    |> Dagger.Query.pipeline(name: "Test")
    |> Dagger.Query.container([])
    |> Dagger.Container.from(address: elixir_image)
    |> Dagger.Container.with_mounted_directory(path: "/dagger_ex", source: repo)
    |> Dagger.Container.with_workdir(path: "/dagger_ex")
    |> Dagger.Container.with_env_variable(name: "MIX_ENV", value: "test")
    |> Dagger.Container.with_exec(args: ["mix", "local.rebar", "--force"])
    |> Dagger.Container.with_exec(args: ["mix", "local.hex", "--force"])
    |> Dagger.Container.with_exec(args: ["mix", "deps.get"])
    |> Dagger.Container.with_exec(args: ["mix", "test", "--color"])
    |> Dagger.Container.stdout()
    |> IO.puts()

    Dagger.disconnect(client)
  end
end
