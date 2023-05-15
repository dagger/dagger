defmodule Mix.Tasks.Ci.Test do
  @moduledoc "Test the project with Dagger."

  use Mix.Task

  @ignore [
    ".lexical",
    ".elixir_ls",
    "_build",
    "deps",
    "*.livemd",
    "*.md",
    ".git*",
    "LICENSE"
  ]

  def run(_) do
    Application.ensure_all_started(:dagger_ex)

    elixir_image = "hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim"

    with_session(fn client ->
      repo =
        client
        |> Dagger.Query.host()
        |> Dagger.Host.directory(".", exclude: @ignore)

      base_image =
        client
        |> Dagger.Query.pipeline("Prepare")
        |> Dagger.Query.container()
        |> Dagger.Container.from(elixir_image)
        |> Dagger.Container.with_mounted_directory("/dagger_ex", repo)
        |> Dagger.Container.with_workdir("/dagger_ex")
        |> Dagger.Container.with_exec(["mix", "local.rebar", "--force"])
        |> Dagger.Container.with_exec(["mix", "local.hex", "--force"])
        |> Dagger.Container.with_env_variable("MIX_ENV", "test")
        |> Dagger.Container.with_exec(["mix", "deps.get"])

      [
        Task.async(__MODULE__, :test, [base_image]),
        Task.async(__MODULE__, :check_format, [base_image])
      ]
      |> Task.await_many(:timer.seconds(180))
    end)
  end

  def test(base_image) do
    base_image
    |> Dagger.Container.pipeline("Test")
    |> Dagger.Container.with_exec(["mix", "test", "--color"])
    |> Dagger.Container.stdout()
  end

  def check_format(base_image) do
    base_image
    |> Dagger.Container.pipeline("Check Format")
    |> Dagger.Container.with_exec(["mix", "format", "--check-formatted"])
    |> Dagger.Container.stdout()
  end

  def with_session(fun, opts \\ []) when is_function(fun, 1) do
    case Dagger.connect(opts) do
      {:ok, client} ->
        result = fun.(client)
        Dagger.disconnect(client)
        result

      {:error, :session_timeout} ->
        raise "Cannot connect to Dagger engine due to session connect timed out."
    end
  end
end
