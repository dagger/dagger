# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.ModuleSource do
  @moduledoc "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc ""
    @spec as_git_source(t()) :: {:ok, Dagger.GitModuleSource.t() | nil} | {:error, term()}
    def as_git_source(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "asGitSource")

      case execute(selection, module_source.client) do
        {:ok, nil} -> {:ok, nil}
        {:ok, data} -> Nestru.decode_from_map(data, Dagger.GitModuleSource)
        error -> error
      end
    end
  )

  (
    @doc ""
    @spec as_local_source(t()) :: {:ok, Dagger.LocalModuleSource.t() | nil} | {:error, term()}
    def as_local_source(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "asLocalSource")

      case execute(selection, module_source.client) do
        {:ok, nil} -> {:ok, nil}
        {:ok, data} -> Nestru.decode_from_map(data, Dagger.LocalModuleSource)
        error -> error
      end
    end
  )

  (
    @doc "Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation"
    @spec as_module(t()) :: Dagger.Module.t()
    def as_module(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "asModule")
      %Dagger.Module{selection: selection, client: module_source.client}
    end
  )

  (
    @doc "A human readable ref string to this module source."
    @spec as_string(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def as_string(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "asString")
      execute(selection, module_source.client)
    end
  )

  (
    @doc "## Required Arguments\n\n* `dep` -"
    @spec dependency(t(), Dagger.ModuleSource.t()) :: Dagger.ModuleSource.t()
    def dependency(%__MODULE__{} = module_source, dep) do
      selection = select(module_source.selection, "dependency")
      selection = arg(selection, "dep", dep)
      %Dagger.ModuleSource{selection: selection, client: module_source.client}
    end
  )

  (
    @doc "A unique identifier for this ModuleSource."
    @spec id(t()) :: {:ok, Dagger.ModuleSourceID.t()} | {:error, term()}
    def id(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "id")
      execute(selection, module_source.client)
    end
  )

  (
    @doc ""
    @spec kind(t()) :: {:ok, Dagger.ModuleSourceKind.t()} | {:error, term()}
    def kind(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "kind")
      execute(selection, module_source.client)
    end
  )

  (
    @doc ""
    @spec root_directory(t()) :: Dagger.Directory.t()
    def root_directory(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "rootDirectory")
      %Dagger.Directory{selection: selection, client: module_source.client}
    end
  )

  (
    @doc "The path to the module subdirectory containing the actual module's source code."
    @spec source_subpath(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def source_subpath(%__MODULE__{} = module_source) do
      selection = select(module_source.selection, "sourceSubpath")
      execute(selection, module_source.client)
    end
  )
end
