# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.ModuleSource do
  @moduledoc "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc."

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID
  @derive Dagger.Sync
  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation"
  @spec as_module(t()) :: Dagger.Module.t()
  def as_module(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("asModule")

    %Dagger.Module{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "A human readable ref string representation of this module source."
  @spec as_string(t()) :: {:ok, String.t()} | {:error, term()}
  def as_string(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("asString")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The ref to clone the root of the git repo from. Only valid for git sources."
  @spec clone_ref(t()) :: {:ok, String.t()} | {:error, term()}
  def clone_ref(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("cloneRef")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The resolved commit of the git repo this source points to. Only valid for git sources."
  @spec commit(t()) :: {:ok, String.t()} | {:error, term()}
  def commit(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("commit")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The clients generated for the module."
  @spec config_clients(t()) :: {:ok, [Dagger.ModuleConfigClient.t()]} | {:error, term()}
  def config_clients(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("configClients") |> QB.select("id")

    with {:ok, items} <- Client.execute(module_source.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.ModuleConfigClient{
           query_builder:
             QB.query()
             |> QB.select("loadModuleConfigClientFromID")
             |> QB.put_arg("id", id),
           client: module_source.client
         }
       end}
    end
  end

  @doc "Whether an existing dagger.json for the module was found."
  @spec config_exists(t()) :: {:ok, boolean()} | {:error, term()}
  def config_exists(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("configExists")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The full directory loaded for the module source, including the source code as a subdirectory."
  @spec context_directory(t()) :: Dagger.Directory.t()
  def context_directory(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("contextDirectory")

    %Dagger.Directory{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "The dependencies of the module source."
  @spec dependencies(t()) :: {:ok, [Dagger.ModuleSource.t()]} | {:error, term()}
  def dependencies(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("dependencies") |> QB.select("id")

    with {:ok, items} <- Client.execute(module_source.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.ModuleSource{
           query_builder:
             QB.query()
             |> QB.select("loadModuleSourceFromID")
             |> QB.put_arg("id", id),
           client: module_source.client
         }
       end}
    end
  end

  @doc "A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance."
  @spec digest(t()) :: {:ok, String.t()} | {:error, term()}
  def digest(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("digest")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The directory containing the module configuration and source code (source code may be in a subdir)."
  @spec directory(t(), String.t()) :: Dagger.Directory.t()
  def directory(%__MODULE__{} = module_source, path) do
    query_builder =
      module_source.query_builder |> QB.select("directory") |> QB.put_arg("path", path)

    %Dagger.Directory{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "The engine version of the module."
  @spec engine_version(t()) :: {:ok, String.t()} | {:error, term()}
  def engine_version(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("engineVersion")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The generated files and directories made on top of the module source's context directory."
  @spec generated_context_directory(t()) :: Dagger.Directory.t()
  def generated_context_directory(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("generatedContextDirectory")

    %Dagger.Directory{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket). Only valid for git sources."
  @spec html_repo_url(t()) :: {:ok, String.t()} | {:error, term()}
  def html_repo_url(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("htmlRepoURL")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The URL to the source's git repo in a web browser. Only valid for git sources."
  @spec html_url(t()) :: {:ok, String.t()} | {:error, term()}
  def html_url(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("htmlURL")

    Client.execute(module_source.client, query_builder)
  end

  @doc "A unique identifier for this ModuleSource."
  @spec id(t()) :: {:ok, Dagger.ModuleSourceID.t()} | {:error, term()}
  def id(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("id")

    Client.execute(module_source.client, query_builder)
  end

  @doc "A JSON file of the GraphQL schema of every dependencies installed in this module"
  @spec introspection_json_file(t(), [{:include_self, boolean() | nil}]) :: Dagger.File.t()
  def introspection_json_file(%__MODULE__{} = module_source, optional_args \\ []) do
    query_builder =
      module_source.query_builder
      |> QB.select("introspectionJSONFile")
      |> QB.maybe_put_arg("includeSelf", optional_args[:include_self])

    %Dagger.File{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "The kind of module source (currently local, git or dir)."
  @spec kind(t()) :: {:ok, Dagger.ModuleSourceKind.t()} | {:error, term()}
  def kind(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("kind")

    case Client.execute(module_source.client, query_builder) do
      {:ok, enum} -> {:ok, Dagger.ModuleSourceKind.from_string(enum)}
      error -> error
    end
  end

  @doc "The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources."
  @spec local_context_directory_path(t()) :: {:ok, String.t()} | {:error, term()}
  def local_context_directory_path(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("localContextDirectoryPath")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The name of the module, including any setting via the withName API."
  @spec module_name(t()) :: {:ok, String.t()} | {:error, term()}
  def module_name(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("moduleName")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The original name of the module as read from the module's dagger.json (or set for the first time with the withName API)."
  @spec module_original_name(t()) :: {:ok, String.t()} | {:error, term()}
  def module_original_name(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("moduleOriginalName")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The original subpath used when instantiating this module source, relative to the context directory."
  @spec original_subpath(t()) :: {:ok, String.t()} | {:error, term()}
  def original_subpath(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("originalSubpath")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The pinned version of this module source."
  @spec pin(t()) :: {:ok, String.t()} | {:error, term()}
  def pin(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("pin")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The import path corresponding to the root of the git repo this source points to. Only valid for git sources."
  @spec repo_root_path(t()) :: {:ok, String.t()} | {:error, term()}
  def repo_root_path(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("repoRootPath")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The SDK configuration of the module."
  @spec sdk(t()) :: Dagger.SDKConfig.t() | nil
  def sdk(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("sdk")

    %Dagger.SDKConfig{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "The path, relative to the context directory, that contains the module's dagger.json."
  @spec source_root_subpath(t()) :: {:ok, String.t()} | {:error, term()}
  def source_root_subpath(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("sourceRootSubpath")

    Client.execute(module_source.client, query_builder)
  end

  @doc "The path to the directory containing the module's source code, relative to the context directory."
  @spec source_subpath(t()) :: {:ok, String.t()} | {:error, term()}
  def source_subpath(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("sourceSubpath")

    Client.execute(module_source.client, query_builder)
  end

  @doc "Forces evaluation of the module source, including any loading into the engine and associated validation."
  @spec sync(t()) :: {:ok, Dagger.ModuleSource.t()} | {:error, term()}
  def sync(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("sync")

    with {:ok, id} <- Client.execute(module_source.client, query_builder) do
      {:ok,
       %Dagger.ModuleSource{
         query_builder:
           QB.query()
           |> QB.select("loadModuleSourceFromID")
           |> QB.put_arg("id", id),
         client: module_source.client
       }}
    end
  end

  @doc "The specified version of the git repo this source points to. Only valid for git sources."
  @spec version(t()) :: {:ok, String.t()} | {:error, term()}
  def version(%__MODULE__{} = module_source) do
    query_builder =
      module_source.query_builder |> QB.select("version")

    Client.execute(module_source.client, query_builder)
  end

  @doc "Update the module source with a new client to generate."
  @spec with_client(t(), String.t(), String.t(), [{:dev, boolean() | nil}]) ::
          Dagger.ModuleSource.t()
  def with_client(%__MODULE__{} = module_source, generator, output_dir, optional_args \\ []) do
    query_builder =
      module_source.query_builder
      |> QB.select("withClient")
      |> QB.put_arg("generator", generator)
      |> QB.put_arg("outputDir", output_dir)
      |> QB.maybe_put_arg("dev", optional_args[:dev])

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Append the provided dependencies to the module source's dependency list."
  @spec with_dependencies(t(), [Dagger.ModuleSourceID.t()]) :: Dagger.ModuleSource.t()
  def with_dependencies(%__MODULE__{} = module_source, dependencies) do
    query_builder =
      module_source.query_builder
      |> QB.select("withDependencies")
      |> QB.put_arg("dependencies", dependencies)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Upgrade the engine version of the module to the given value."
  @spec with_engine_version(t(), String.t()) :: Dagger.ModuleSource.t()
  def with_engine_version(%__MODULE__{} = module_source, version) do
    query_builder =
      module_source.query_builder
      |> QB.select("withEngineVersion")
      |> QB.put_arg("version", version)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Update the module source with additional include patterns for files+directories from its context that are required for building it"
  @spec with_includes(t(), [String.t()]) :: Dagger.ModuleSource.t()
  def with_includes(%__MODULE__{} = module_source, patterns) do
    query_builder =
      module_source.query_builder |> QB.select("withIncludes") |> QB.put_arg("patterns", patterns)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Update the module source with a new name."
  @spec with_name(t(), String.t()) :: Dagger.ModuleSource.t()
  def with_name(%__MODULE__{} = module_source, name) do
    query_builder =
      module_source.query_builder |> QB.select("withName") |> QB.put_arg("name", name)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Update the module source with a new SDK."
  @spec with_sdk(t(), String.t()) :: Dagger.ModuleSource.t()
  def with_sdk(%__MODULE__{} = module_source, source) do
    query_builder =
      module_source.query_builder |> QB.select("withSDK") |> QB.put_arg("source", source)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Update the module source with a new source subpath."
  @spec with_source_subpath(t(), String.t()) :: Dagger.ModuleSource.t()
  def with_source_subpath(%__MODULE__{} = module_source, path) do
    query_builder =
      module_source.query_builder |> QB.select("withSourceSubpath") |> QB.put_arg("path", path)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Update one or more module dependencies."
  @spec with_update_dependencies(t(), [String.t()]) :: Dagger.ModuleSource.t()
  def with_update_dependencies(%__MODULE__{} = module_source, dependencies) do
    query_builder =
      module_source.query_builder
      |> QB.select("withUpdateDependencies")
      |> QB.put_arg("dependencies", dependencies)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end

  @doc "Remove the provided dependencies from the module source's dependency list."
  @spec without_dependencies(t(), [String.t()]) :: Dagger.ModuleSource.t()
  def without_dependencies(%__MODULE__{} = module_source, dependencies) do
    query_builder =
      module_source.query_builder
      |> QB.select("withoutDependencies")
      |> QB.put_arg("dependencies", dependencies)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: module_source.client
    }
  end
end

defimpl Jason.Encoder, for: Dagger.ModuleSource do
  def encode(module_source, opts) do
    {:ok, id} = Dagger.ModuleSource.id(module_source)
    Jason.Encode.string(id, opts)
  end
end

defimpl Nestru.Decoder, for: Dagger.ModuleSource do
  def decode_fields_hint(_struct, _context, id) do
    {:ok, Dagger.Client.load_module_source_from_id(Dagger.Global.dag(), id)}
  end
end
