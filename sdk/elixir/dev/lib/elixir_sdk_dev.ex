defmodule ElixirSdkDev do
  @moduledoc """
  A module for help developing Elixir SDK.
  """

  use Dagger.Mod.Object, name: "ElixirSdkDev"

  @base_image "elixir:1.18.4-otp-28-alpine@sha256:35777d29cf6c00c66b2c4ae135bf187dd9e3d5d4b75d0922b75e073732a1613a"

  object do
    field :source, Dagger.Directory.t()
    field :container, Dagger.Container.t()
  end

  defn init(
         source:
           {Dagger.Directory.t() | nil,
            doc: "The Elixir SDK source",
            default_path: "..",
            ignore: [
              "**/*",
              "!LICENSE",
              "!README.md",
              # sdk source.
              "!mix.exs",
              "!mix.lock",
              "!.formatter.exs",
              "!.credo.exs",
              "!lib/**/*.ex",
              "!test/support/**/*.ex",
              "!test/**/*.exs",
              "!runtime/go.mod",
              "!runtime/go.sum",
              "!runtime/main.go",
              "!runtime/templates/*",
              # codegen source.
              "!dagger_codegen/mix.exs",
              "!dagger_codegen/mix.lock",
              "!dagger_codegen/.formatter.exs",
              "!dagger_codegen/lib/**/*.ex",
              "!dagger_codegen/test/support/**/*.ex",
              "!dagger_codegen/test/**/*.exs",
              "!dagger_codegen/test/fixtures/**/*.json"
            ]},
         container: Dagger.Container.t() | nil
       ) :: ElixirSdkDev.t() do
    container =
      if is_nil(container) do
        with_base(source)
      else
        container
      end

    %ElixirSdkDev{
      source: source,
      container: container
    }
  end

  @doc """
  Test the SDK.
  """
  defn test(self) :: Dagger.Void.t() do
    [
      Task.async(fn -> sdk_test(self) end),
      Task.async(fn -> codegen_test(self) end)
    ]
    |> Task.await_many(:infinity)
    |> Enum.find(fn result -> match?({:error, _}, result) end)
    |> case do
      nil -> :ok
      error -> error
    end
  end

  @doc """
  Lint the SDK.
  """
  defn lint(self) :: Dagger.Void.t() do
    self.container
    |> Dagger.Container.with_exec(~w"mix credo")
    |> sync()
  end

  @doc """
  Generate the SDK API.
  """
  defn generate(self, introspection_json: Dagger.File.t()) ::
         Dagger.Directory.t() do
    gen =
      self
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
  defn sdk_test(self) :: Dagger.Void.t() do
    self.container
    |> Dagger.Container.with_exec(~w"mix test")
    |> sync()
  end

  @doc """
  Run dagger_codegen tests.
  """
  defn codegen_test(self) :: Dagger.Void.t() do
    self
    |> with_codegen()
    |> Dagger.Container.with_exec(~w"mix test")
    |> sync()
  end

  @doc """
  Sync Elixir image to keep both dev and runtime modules consistent.
  """
  defn sync_image(self) :: Dagger.File.t() do
    path = "runtime/main.go"

    {:ok, runtime_main_go} =
      self.source
      |> with_base()
      |> Dagger.Container.file(path)
      |> Dagger.File.contents()

    new_runtime_main_go =
      Regex.replace(
        ~r/elixirImage\s*=.*\n/,
        runtime_main_go,
        "elixirImage = \"#{@base_image}\"\n"
      )

    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("golang:1.23-alpine")
    |> Dagger.Container.with_new_file(path, new_runtime_main_go)
    |> Dagger.Container.with_exec(["go", "fmt", path])
    |> Dagger.Container.file(path)
  end

  defn with_codegen(self) :: Dagger.Container.t() do
    self.container
    |> Dagger.Container.with_workdir("dagger_codegen")
    |> Dagger.Container.with_exec(~w"mix deps.get")
  end

  defp with_base(source) do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from(@base_image)
    |> Dagger.Container.with_workdir("/sdk/elixir")
    |> Dagger.Container.with_directory(".", source)
    |> Dagger.Container.with_exec(~w"mix local.hex --force")
    |> Dagger.Container.with_exec(~w"mix local.rebar --force")
    |> Dagger.Container.with_exec(~w"mix deps.get")
  end

  defp sync(container) do
    container
    |> Dagger.Container.sync()
    |> case do
      {:ok, _} -> :ok
      error -> error
    end
  end
end
