defmodule ElixirSdkDev do
  @moduledoc """
  A module for developing Elixir SDK.
  """

  use Dagger.Mod.Object, name: "ElixirSdkDev"

  @images %{
    "1.15" =>
      "hexpm/elixir:1.15.8-erlang-26.2.5.2-debian-bookworm-20240701-slim@sha256:7f282f3b1a50d795375f5bb95250aeec36d21dc2b56f6fba45b88243ac001e52",
    "1.16" =>
      "hexpm/elixir:1.16.2-erlang-26.2.5-debian-bookworm-20240513-slim@sha256:4c3bcf223c896bd817484569164357a49c473556e8773d74a591a3c565e8b8b9",
    "1.17" =>
      "hexpm/elixir:1.17.2-erlang-27.0.1-debian-bookworm-20240701-slim@sha256:0e4234e482dd487c78d0f0b73fa9bc9b03ccad0d964ef0e7a5e92a6df68ab289"
  }

  @latest_version "1.17"

  @doc """
  Lint the generated sources.
  """
  defn lint(src: Dagger.Directory.t()) :: Dagger.Container.t() do
    base(src, @images[@latest_version])
    |> Dagger.Container.with_exec(~w"mix lint")
    |> Dagger.Container.sync()
  end

  defp base(src, base_image) do
    dot_mix_cache =
      dag()
      |> Dagger.Client.cache_volume("dot-mix")

    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from(base_image)
    |> Dagger.Container.with_mounted_cache("/root/.mix", dot_mix_cache)
    |> Dagger.Container.with_directory("/sdk/elixir", src)
    |> Dagger.Container.with_workdir("/sdk/elixir")
    |> Dagger.Container.with_exec(~w" mix local.hex --force")
    |> Dagger.Container.with_exec(~w" mix local.rebar --force")
    |> Dagger.Container.with_exec(~w" mix deps.get")
  end
end
