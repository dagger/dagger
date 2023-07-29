# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Directory do
  @moduledoc "A directory."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "Gets the difference between this directory and an another directory.\n\n## Required Arguments\n\n* `other` - Identifier of the directory to compare."
    @spec diff(t(), Dagger.Directory.t()) :: Dagger.Directory.t()
    def diff(%__MODULE__{} = directory, other) do
      selection = select(directory.selection, "diff")

      (
        {:ok, id} = Dagger.Directory.id(other)
        selection = arg(selection, "other", id)
      )

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves a directory at the given path.\n\n## Required Arguments\n\n* `path` - Location of the directory to retrieve (e.g., \"/src\")."
    @spec directory(t(), String.t()) :: Dagger.Directory.t()
    def directory(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "directory")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Builds a new Docker container from this directory.\n\n\n\n## Optional Arguments\n\n* `dockerfile` - Path to the Dockerfile to use (e.g., \"frontend.Dockerfile\").\n\nDefaults: './Dockerfile'.\n* `platform` - The platform to build.\n* `build_args` - Build arguments to use in the build.\n* `target` - Target build stage to build.\n* `secrets` - Secrets to pass to the build.\n\nThey will be mounted at /run/secrets/[secret-name]."
    @spec docker_build(t(), keyword()) :: Dagger.Container.t()
    def docker_build(%__MODULE__{} = directory, optional_args \\ []) do
      selection = select(directory.selection, "dockerBuild")

      selection =
        if is_nil(optional_args[:dockerfile]) do
          selection
        else
          arg(selection, "dockerfile", optional_args[:dockerfile])
        end

      selection =
        if is_nil(optional_args[:platform]) do
          selection
        else
          arg(selection, "platform", optional_args[:platform])
        end

      selection =
        if is_nil(optional_args[:build_args]) do
          selection
        else
          arg(selection, "buildArgs", optional_args[:build_args])
        end

      selection =
        if is_nil(optional_args[:target]) do
          selection
        else
          arg(selection, "target", optional_args[:target])
        end

      selection =
        if is_nil(optional_args[:secrets]) do
          selection
        else
          arg(selection, "secrets", optional_args[:secrets])
        end

      %Dagger.Container{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Returns a list of files and directories at the given path.\n\n\n\n## Optional Arguments\n\n* `path` - Location of the directory to look at (e.g., \"/src\")."
    @spec entries(t(), keyword()) :: {:ok, [String.t()]} | {:error, term()}
    def entries(%__MODULE__{} = directory, optional_args \\ []) do
      selection = select(directory.selection, "entries")

      selection =
        if is_nil(optional_args[:path]) do
          selection
        else
          arg(selection, "path", optional_args[:path])
        end

      execute(selection, directory.client)
    end
  )

  (
    @doc "Writes the contents of the directory to a path on the host.\n\n## Required Arguments\n\n* `path` - Location of the copied directory (e.g., \"logs/\")."
    @spec export(t(), String.t()) :: {:ok, boolean()} | {:error, term()}
    def export(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "export")
      selection = arg(selection, "path", path)
      execute(selection, directory.client)
    end
  )

  (
    @doc "Retrieves a file at the given path.\n\n## Required Arguments\n\n* `path` - Location of the file to retrieve (e.g., \"README.md\")."
    @spec file(t(), String.t()) :: Dagger.File.t()
    def file(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "file")
      selection = arg(selection, "path", path)
      %Dagger.File{selection: selection, client: directory.client}
    end
  )

  (
    @doc "The content-addressed identifier of the directory."
    @spec id(t()) :: {:ok, Dagger.DirectoryID.t()} | {:error, term()}
    def id(%__MODULE__{} = directory) do
      selection = select(directory.selection, "id")
      execute(selection, directory.client)
    end
  )

  (
    @doc "Creates a named sub-pipeline\n\n## Required Arguments\n\n* `name` - Pipeline name.\n\n## Optional Arguments\n\n* `description` - Pipeline description.\n* `labels` - Pipeline labels."
    @spec pipeline(t(), String.t(), keyword()) :: Dagger.Directory.t()
    def pipeline(%__MODULE__{} = directory, name, optional_args \\ []) do
      selection = select(directory.selection, "pipeline")
      selection = arg(selection, "name", name)

      selection =
        if is_nil(optional_args[:description]) do
          selection
        else
          arg(selection, "description", optional_args[:description])
        end

      selection =
        if is_nil(optional_args[:labels]) do
          selection
        else
          arg(selection, "labels", optional_args[:labels])
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a directory written at the given path.\n\n## Required Arguments\n\n* `path` - Location of the written directory (e.g., \"/src/\").\n* `directory` - Identifier of the directory to copy.\n\n## Optional Arguments\n\n* `exclude` - Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).\n* `include` - Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"])."
    @spec with_directory(t(), String.t(), Dagger.Directory.t(), keyword()) :: Dagger.Directory.t()
    def with_directory(%__MODULE__{} = directory, path, directory, optional_args \\ []) do
      selection = select(directory.selection, "withDirectory")
      selection = arg(selection, "path", path)

      (
        {:ok, id} = Dagger.Directory.id(directory)
        selection = arg(selection, "directory", id)
      )

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

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus the contents of the given file copied to the given path.\n\n## Required Arguments\n\n* `path` - Location of the copied file (e.g., \"/file.txt\").\n* `source` - Identifier of the file to copy.\n\n## Optional Arguments\n\n* `permissions` - Permission given to the copied file (e.g., 0600).\n\nDefault: 0644."
    @spec with_file(t(), String.t(), Dagger.File.t(), keyword()) :: Dagger.Directory.t()
    def with_file(%__MODULE__{} = directory, path, source, optional_args \\ []) do
      selection = select(directory.selection, "withFile")
      selection = arg(selection, "path", path)

      (
        {:ok, id} = Dagger.File.id(source)
        selection = arg(selection, "source", id)
      )

      selection =
        if is_nil(optional_args[:permissions]) do
          selection
        else
          arg(selection, "permissions", optional_args[:permissions])
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a new directory created at the given path.\n\n## Required Arguments\n\n* `path` - Location of the directory created (e.g., \"/logs\").\n\n## Optional Arguments\n\n* `permissions` - Permission granted to the created directory (e.g., 0777).\n\nDefault: 0755."
    @spec with_new_directory(t(), String.t(), keyword()) :: Dagger.Directory.t()
    def with_new_directory(%__MODULE__{} = directory, path, optional_args \\ []) do
      selection = select(directory.selection, "withNewDirectory")
      selection = arg(selection, "path", path)

      selection =
        if is_nil(optional_args[:permissions]) do
          selection
        else
          arg(selection, "permissions", optional_args[:permissions])
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a new file written at the given path.\n\n## Required Arguments\n\n* `path` - Location of the written file (e.g., \"/file.txt\").\n* `contents` - Content of the written file (e.g., \"Hello world!\").\n\n## Optional Arguments\n\n* `permissions` - Permission given to the copied file (e.g., 0600).\n\nDefault: 0644."
    @spec with_new_file(t(), String.t(), String.t(), keyword()) :: Dagger.Directory.t()
    def with_new_file(%__MODULE__{} = directory, path, contents, optional_args \\ []) do
      selection = select(directory.selection, "withNewFile")
      selection = arg(selection, "path", path)
      selection = arg(selection, "contents", contents)

      selection =
        if is_nil(optional_args[:permissions]) do
          selection
        else
          arg(selection, "permissions", optional_args[:permissions])
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with all file/dir timestamps set to the given time.\n\n## Required Arguments\n\n* `timestamp` - Timestamp to set dir/files in.\n\nFormatted in seconds following Unix epoch (e.g., 1672531199)."
    @spec with_timestamps(t(), integer()) :: Dagger.Directory.t()
    def with_timestamps(%__MODULE__{} = directory, timestamp) do
      selection = select(directory.selection, "withTimestamps")
      selection = arg(selection, "timestamp", timestamp)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with the directory at the given path removed.\n\n## Required Arguments\n\n* `path` - Location of the directory to remove (e.g., \".github/\")."
    @spec without_directory(t(), String.t()) :: Dagger.Directory.t()
    def without_directory(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "withoutDirectory")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with the file at the given path removed.\n\n## Required Arguments\n\n* `path` - Location of the file to remove (e.g., \"/file.txt\")."
    @spec without_file(t(), String.t()) :: Dagger.Directory.t()
    def without_file(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "withoutFile")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )
end
