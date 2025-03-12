defmodule ElixirSdkDev do
  @moduledoc """
  A module for help developing Elixir SDK.
  """

  use Dagger.Mod.Object, name: "ElixirSdkDev"

  @base_image "hexpm/elixir:1.17.3-erlang-27.2-alpine-3.20.3@sha256:557156f12d23b0d2aa12d8955668cc3b9a981563690bb9ecabd7a5a951702afe"

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

  @doc """
  Generate the SDK API.
  """
  defn generate(container: Dagger.Container.t(), introspection_json: Dagger.File.t()) ::
         Dagger.Directory.t() do
    gen =
      container
      |> with_codegen()
      |> Dagger.Container.with_mounted_file("/schema.json", introspection_json)
      |> Dagger.Container.with_exec(
        ~w"mix dagger.codegen generate --introspection /schema.json --outdir gen"
      )
      |> Dagger.Container.with_exec(~w"mix format gen/*.ex")
      |> Dagger.Container.directory("gen")

    dag()
    |> Dagger.Client.directory()
    |> Dagger.Directory.with_directory("sdk/elixir/lib/dagger/gen", gen)
  end

  @doc """
  Run the SDK tests.
  """
  defn sdk_test(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> Dagger.Container.with_exec(~w"mix test")
  end

  @doc """
  Run dagger_codegen tests.
  """
  defn codegen_test(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> with_codegen()
    |> Dagger.Container.with_exec(~w"mix test")
  end

  @doc """
  Sync Elixir image to keep both dev and runtime modules consistent.
  """
  defn sync_image(
         source: {Dagger.Directory.t(), doc: "The Elixir SDK source", default_path: ".."}
       ) :: Dagger.File.t() do
    path = "runtime/main.go"

    {:ok, runtime_main_go} =
      source
      |> with_base()
      |> Dagger.Container.file(path)
      |> Dagger.File.contents()

    new_runtime_main_go =
      Regex.replace(~r/elixirImage\s*=.*\n/, runtime_main_go, "elixirImage = \"#{@base_image}\"\n")

    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("golang:1.23-alpine")
    |> Dagger.Container.with_new_file(path, new_runtime_main_go)
    |> Dagger.Container.with_exec(["go", "fmt", path])
    |> Dagger.Container.file(path)
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

  defn with_codegen(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
    |> Dagger.Container.with_workdir("dagger_codegen")
    |> Dagger.Container.with_exec(~w"mix deps.get")
  end
end
