# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Host do
  @moduledoc "Information about the host execution environment."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "Accesses a directory on the host.\n\n## Required Arguments\n\n* `path` - Location of the directory to access (e.g., \".\").\n\n## Optional Arguments\n\n* `exclude` - Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).\n* `include` - Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"])."
    @spec directory(t(), String.t(), keyword()) :: Dagger.Directory.t()
    def directory(%__MODULE__{} = host, path, optional_args \\ []) do
      selection = select(host.selection, "directory")
      selection = arg(selection, "path", path)

      selection =
        if is_nil(optional_args[:exclude]) do
          selection
        else
          arg(selection, "exclude", optional_args[:exclude])
        end

      selection =
        if is_nil(optional_args[:include]) do
          selection
        else
          arg(selection, "include", optional_args[:include])
        end

      %Dagger.Directory{selection: selection, client: host.client}
    end
  )

  (
    @doc "Accesses an environment variable on the host.\n\n## Required Arguments\n\n* `name` - Name of the environment variable (e.g., \"PATH\")."
    @spec env_variable(t(), String.t()) :: {:ok, Dagger.HostVariable.t() | nil} | {:error, term()}
    def env_variable(%__MODULE__{} = host, name) do
      selection = select(host.selection, "envVariable")
      selection = arg(selection, "name", name)

      case execute(selection, host.client) do
        {:ok, nil} -> {:ok, nil}
        {:ok, data} -> Nestru.decode_from_map(data, Dagger.HostVariable)
        error -> error
      end
    end
  )

  (
    @doc "Accesses a file on the host.\n\n## Required Arguments\n\n* `path` - Location of the file to retrieve (e.g., \"README.md\")."
    @spec file(t(), String.t()) :: Dagger.File.t()
    def file(%__MODULE__{} = host, path) do
      selection = select(host.selection, "file")
      selection = arg(selection, "path", path)
      %Dagger.File{selection: selection, client: host.client}
    end
  )

  (
    @doc "Accesses a Unix socket on the host.\n\n## Required Arguments\n\n* `path` - Location of the Unix socket (e.g., \"/var/run/docker.sock\")."
    @spec unix_socket(t(), String.t()) :: Dagger.Socket.t()
    def unix_socket(%__MODULE__{} = host, path) do
      selection = select(host.selection, "unixSocket")
      selection = arg(selection, "path", path)
      %Dagger.Socket{selection: selection, client: host.client}
    end
  )

  (
    @doc "Retrieves the current working directory on the host.\n\n\n\n## Optional Arguments\n\n* `exclude` - Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).\n* `include` - Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"])."
    @deprecated "Use `directory` with path set to '.' instead"
    @spec workdir(t(), keyword()) :: Dagger.Directory.t()
    def workdir(%__MODULE__{} = host, optional_args \\ []) do
      selection = select(host.selection, "workdir")

      selection =
        if is_nil(optional_args[:exclude]) do
          selection
        else
          arg(selection, "exclude", optional_args[:exclude])
        end

      selection =
        if is_nil(optional_args[:include]) do
          selection
        else
          arg(selection, "include", optional_args[:include])
        end

      %Dagger.Directory{selection: selection, client: host.client}
    end
  )
end
