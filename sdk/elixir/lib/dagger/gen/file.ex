# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.File do
  @moduledoc "A file."

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID
  @derive Dagger.Sync
  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "Retrieves the contents of the file."
  @spec contents(t()) :: {:ok, String.t()} | {:error, term()}
  def contents(%__MODULE__{} = file) do
    query_builder =
      file.query_builder |> QB.select("contents")

    Client.execute(file.client, query_builder)
  end

  @doc "Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine."
  @spec digest(t(), [{:exclude_metadata, boolean() | nil}]) ::
          {:ok, String.t()} | {:error, term()}
  def digest(%__MODULE__{} = file, optional_args \\ []) do
    query_builder =
      file.query_builder
      |> QB.select("digest")
      |> QB.maybe_put_arg("excludeMetadata", optional_args[:exclude_metadata])

    Client.execute(file.client, query_builder)
  end

  @doc "Writes the file to a file path on the current runtime container spawned by Dagger engine."
  @spec export(t(), String.t(), [{:allow_parent_dir_path, boolean() | nil}]) ::
          {:ok, String.t()} | {:error, term()}
  def export(%__MODULE__{} = file, path, optional_args \\ []) do
    query_builder =
      file.query_builder
      |> QB.select("export")
      |> QB.put_arg("path", path)
      |> QB.maybe_put_arg("allowParentDirPath", optional_args[:allow_parent_dir_path])

    Client.execute(file.client, query_builder)
  end

  @doc "A unique identifier for this File."
  @spec id(t()) :: {:ok, Dagger.FileID.t()} | {:error, term()}
  def id(%__MODULE__{} = file) do
    query_builder =
      file.query_builder |> QB.select("id")

    Client.execute(file.client, query_builder)
  end

  @doc "Retrieves the name of the file."
  @spec name(t()) :: {:ok, String.t()} | {:error, term()}
  def name(%__MODULE__{} = file) do
    query_builder =
      file.query_builder |> QB.select("name")

    Client.execute(file.client, query_builder)
  end

  @doc "Retrieves the size of the file, in bytes."
  @spec size(t()) :: {:ok, integer()} | {:error, term()}
  def size(%__MODULE__{} = file) do
    query_builder =
      file.query_builder |> QB.select("size")

    Client.execute(file.client, query_builder)
  end

  @doc "Force evaluation in the engine."
  @spec sync(t()) :: {:ok, Dagger.File.t()} | {:error, term()}
  def sync(%__MODULE__{} = file) do
    query_builder =
      file.query_builder |> QB.select("sync")

    with {:ok, id} <- Client.execute(file.client, query_builder) do
      {:ok,
       %Dagger.File{
         query_builder:
           QB.query()
           |> QB.select("loadFileFromID")
           |> QB.put_arg("id", id),
         client: file.client
       }}
    end
  end

  @doc "Retrieves this file with its name set to the given name."
  @spec with_name(t(), String.t()) :: Dagger.File.t()
  def with_name(%__MODULE__{} = file, name) do
    query_builder =
      file.query_builder |> QB.select("withName") |> QB.put_arg("name", name)

    %Dagger.File{
      query_builder: query_builder,
      client: file.client
    }
  end

  @doc "Retrieves this file with its created/modified timestamps set to the given time."
  @spec with_timestamps(t(), integer()) :: Dagger.File.t()
  def with_timestamps(%__MODULE__{} = file, timestamp) do
    query_builder =
      file.query_builder |> QB.select("withTimestamps") |> QB.put_arg("timestamp", timestamp)

    %Dagger.File{
      query_builder: query_builder,
      client: file.client
    }
  end
end
