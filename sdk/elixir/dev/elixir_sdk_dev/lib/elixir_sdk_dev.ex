defmodule ElixirSdkDev do
  @moduledoc """
  A module for help developing Elixir SDK.
  """

  use Dagger.Mod.Object, name: "ElixirSdkDev"

  @base_image "hexpm/elixir:1.17.2-erlang-27.0.1-alpine-3.20.2@sha256:7c8a13cbff321b7d6f54b4c9a21a10fc8b987974171231eaa77532b8e638b645"

  @doc """
  Test the SDK.
  """
  defn test(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> sdk_test()
    |> codegen_test()
  end

  @doc """
  Lint the SDK.
  """
  defn lint(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> Dagger.Container.with_exec(~w"mix credo")
  end

  defn sdk_test(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> Dagger.Container.with_exec(~w"mix test")
  end

  defn codegen_test(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> Dagger.Container.with_workdir("dagger_codegen")
    |> Dagger.Container.with_exec(~w"mix deps.get")
    |> Dagger.Container.with_exec(~w"mix test")
  end

  defn with_base(source: {Dagger.Directory.t(), doc: "The Elixir SDK source"}) ::
         Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from(@base_image)
    |> Dagger.Container.with_workdir("/sdk")
    |> Dagger.Container.with_directory(".", source)
    |> Dagger.Container.with_exec(~w"mix local.hex --force")
    |> Dagger.Container.with_exec(~w"mix local.rebar --force")
    |> Dagger.Container.with_exec(~w"mix deps.get")
  end
end
