# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.LocalModuleSource do
  @moduledoc "Module source that that originates from a path locally relative to an arbitrary directory."

  use Dagger.Core.QueryBuilder

  @derive Dagger.ID

  defstruct [:selection, :client]

  @type t() :: %__MODULE__{}

  @doc "The directory containing everything needed to load load and use the module."
  @spec context_directory(t()) :: Dagger.Directory.t() | nil
  def context_directory(%__MODULE__{} = local_module_source) do
    selection =
      local_module_source.selection |> select("contextDirectory")

    %Dagger.Directory{
      selection: selection,
      client: local_module_source.client
    }
  end

  @doc "A unique identifier for this LocalModuleSource."
  @spec id(t()) :: {:ok, Dagger.LocalModuleSourceID.t()} | {:error, term()}
  def id(%__MODULE__{} = local_module_source) do
    selection =
      local_module_source.selection |> select("id")

    execute(selection, local_module_source.client)
  end

  @doc "The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory)."
  @spec root_subpath(t()) :: {:ok, String.t()} | {:error, term()}
  def root_subpath(%__MODULE__{} = local_module_source) do
    selection =
      local_module_source.selection |> select("rootSubpath")

    execute(selection, local_module_source.client)
  end
end
