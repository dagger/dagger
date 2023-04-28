# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Directory do
  @moduledoc "A directory."
  use Dagger.QueryBuilder
  defstruct [:selection, :client]

  (
    @doc "Gets the difference between this directory and an another directory.\n\n## Required Arguments\n\n* `other` - Identifier of the directory to compare.\n\n## Optional Arguments"
    def diff(%__MODULE__{} = directory, other) do
      selection = select(directory.selection, "diff")
      selection = arg(selection, "other", other)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves a directory at the given path.\n\n## Required Arguments\n\n* `path` - Location of the directory to retrieve (e.g., \"/src\").\n\n## Optional Arguments"
    def directory(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "directory")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Builds a new Docker container from this directory.\n\n## Required Arguments\n\n\n\n## Optional Arguments\n\n* `dockerfile` - Path to the Dockerfile to use (e.g., \"frontend.Dockerfile\").\n\nDefaults: './Dockerfile'.\n* `platform` - The platform to build.\n* `build_args` - Build arguments to use in the build.\n* `target` - Target build stage to build."
    def docker_build(%__MODULE__{} = directory, optional_args \\ []) do
      selection = select(directory.selection, "dockerBuild")

      selection =
        if not is_nil(optional_args[:dockerfile]) do
          arg(selection, "dockerfile", optional_args[:dockerfile])
        else
          selection
        end

      selection =
        if not is_nil(optional_args[:platform]) do
          arg(selection, "platform", optional_args[:platform])
        else
          selection
        end

      selection =
        if not is_nil(optional_args[:build_args]) do
          arg(selection, "buildArgs", optional_args[:build_args])
        else
          selection
        end

      selection =
        if not is_nil(optional_args[:target]) do
          arg(selection, "target", optional_args[:target])
        else
          selection
        end

      %Dagger.Container{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Returns a list of files and directories at the given path.\n\n## Required Arguments\n\n\n\n## Optional Arguments\n\n* `path` - Location of the directory to look at (e.g., \"/src\")."
    def entries(%__MODULE__{} = directory, optional_args \\ []) do
      selection = select(directory.selection, "entries")

      selection =
        if not is_nil(optional_args[:path]) do
          arg(selection, "path", optional_args[:path])
        else
          selection
        end

      execute(selection, directory.client)
    end
  )

  (
    @doc "Writes the contents of the directory to a path on the host.\n\n## Required Arguments\n\n* `path` - Location of the copied directory (e.g., \"logs/\").\n\n## Optional Arguments"
    def export(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "export")
      selection = arg(selection, "path", path)
      execute(selection, directory.client)
    end
  )

  (
    @doc "Retrieves a file at the given path.\n\n## Required Arguments\n\n* `path` - Location of the file to retrieve (e.g., \"README.md\").\n\n## Optional Arguments"
    def file(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "file")
      selection = arg(selection, "path", path)
      %Dagger.File{selection: selection, client: directory.client}
    end
  )

  (
    @doc "The content-addressed identifier of the directory.\n\n## Required Arguments\n\n\n\n## Optional Arguments"
    def id(%__MODULE__{} = directory) do
      selection = select(directory.selection, "id")
      execute(selection, directory.client)
    end
  )

  (
    @doc "load a project's metadata\n\n## Required Arguments\n\n* `config_path` - \n\n## Optional Arguments"
    def load_project(%__MODULE__{} = directory, config_path) do
      selection = select(directory.selection, "loadProject")
      selection = arg(selection, "configPath", config_path)
      %Dagger.Project{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Creates a named sub-pipeline\n\n## Required Arguments\n\n* `name` - Pipeline name.\n\n## Optional Arguments\n\n* `description` - Pipeline description.\n* `labels` - Pipeline labels."
    def pipeline(%__MODULE__{} = directory, name, optional_args \\ []) do
      selection = select(directory.selection, "pipeline")
      selection = arg(selection, "name", name)

      selection =
        if not is_nil(optional_args[:description]) do
          arg(selection, "description", optional_args[:description])
        else
          selection
        end

      selection =
        if not is_nil(optional_args[:labels]) do
          arg(selection, "labels", optional_args[:labels])
        else
          selection
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a directory written at the given path.\n\n## Required Arguments\n\n* `path` - Location of the written directory (e.g., \"/src/\").\n* `directory` - Identifier of the directory to copy.\n\n## Optional Arguments\n\n* `exclude` - Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).\n* `include` - Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"])."
    def with_directory(%__MODULE__{} = directory, path, directory, optional_args \\ []) do
      selection = select(directory.selection, "withDirectory")
      selection = arg(selection, "path", path)
      selection = arg(selection, "directory", directory)

      selection =
        if not is_nil(optional_args[:exclude]) do
          arg(selection, "exclude", optional_args[:exclude])
        else
          selection
        end

      selection =
        if not is_nil(optional_args[:include]) do
          arg(selection, "include", optional_args[:include])
        else
          selection
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus the contents of the given file copied to the given path.\n\n## Required Arguments\n\n* `path` - Location of the copied file (e.g., \"/file.txt\").\n* `source` - Identifier of the file to copy.\n\n## Optional Arguments\n\n* `permissions` - Permission given to the copied file (e.g., 0600).\n\nDefault: 0644."
    def with_file(%__MODULE__{} = directory, path, source, optional_args \\ []) do
      selection = select(directory.selection, "withFile")
      selection = arg(selection, "path", path)
      selection = arg(selection, "source", source)

      selection =
        if not is_nil(optional_args[:permissions]) do
          arg(selection, "permissions", optional_args[:permissions])
        else
          selection
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a new directory created at the given path.\n\n## Required Arguments\n\n* `path` - Location of the directory created (e.g., \"/logs\").\n\n## Optional Arguments\n\n* `permissions` - Permission granted to the created directory (e.g., 0777).\n\nDefault: 0755."
    def with_new_directory(%__MODULE__{} = directory, path, optional_args \\ []) do
      selection = select(directory.selection, "withNewDirectory")
      selection = arg(selection, "path", path)

      selection =
        if not is_nil(optional_args[:permissions]) do
          arg(selection, "permissions", optional_args[:permissions])
        else
          selection
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory plus a new file written at the given path.\n\n## Required Arguments\n\n* `path` - Location of the written file (e.g., \"/file.txt\").\n* `contents` - Content of the written file (e.g., \"Hello world!\").\n\n## Optional Arguments\n\n* `permissions` - Permission given to the copied file (e.g., 0600).\n\nDefault: 0644."
    def with_new_file(%__MODULE__{} = directory, path, contents, optional_args \\ []) do
      selection = select(directory.selection, "withNewFile")
      selection = arg(selection, "path", path)
      selection = arg(selection, "contents", contents)

      selection =
        if not is_nil(optional_args[:permissions]) do
          arg(selection, "permissions", optional_args[:permissions])
        else
          selection
        end

      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with all file/dir timestamps set to the given time.\n\n## Required Arguments\n\n* `timestamp` - Timestamp to set dir/files in.\n\nFormatted in seconds following Unix epoch (e.g., 1672531199).\n\n## Optional Arguments"
    def with_timestamps(%__MODULE__{} = directory, timestamp) do
      selection = select(directory.selection, "withTimestamps")
      selection = arg(selection, "timestamp", timestamp)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with the directory at the given path removed.\n\n## Required Arguments\n\n* `path` - Location of the directory to remove (e.g., \".github/\").\n\n## Optional Arguments"
    def without_directory(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "withoutDirectory")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )

  (
    @doc "Retrieves this directory with the file at the given path removed.\n\n## Required Arguments\n\n* `path` - Location of the file to remove (e.g., \"/file.txt\").\n\n## Optional Arguments"
    def without_file(%__MODULE__{} = directory, path) do
      selection = select(directory.selection, "withoutFile")
      selection = arg(selection, "path", path)
      %Dagger.Directory{selection: selection, client: directory.client}
    end
  )
end
