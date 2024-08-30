# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Container do
  @moduledoc "An OCI-compatible container, also known as a Docker container."

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID
  @derive Dagger.Sync
  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc """
  Turn the container into a Service.

  Be sure to set any exposed ports before this conversion.
  """
  @spec as_service(t()) :: Dagger.Service.t()
  def as_service(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("asService")

    %Dagger.Service{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Returns a File representing the container serialized to a tarball."
  @spec as_tarball(t(), [
          {:platform_variants, [Dagger.ContainerID.t()]},
          {:forced_compression, Dagger.ImageLayerCompression.t() | nil},
          {:media_types, Dagger.ImageMediaTypes.t() | nil}
        ]) :: Dagger.File.t()
  def as_tarball(%__MODULE__{} = container, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("asTarball")
      |> QB.maybe_put_arg(
        "platformVariants",
        if(optional_args[:platform_variants],
          do: Enum.map(optional_args[:platform_variants], &Dagger.ID.id!/1),
          else: nil
        )
      )
      |> QB.maybe_put_arg("forcedCompression", optional_args[:forced_compression])
      |> QB.maybe_put_arg("mediaTypes", optional_args[:media_types])

    %Dagger.File{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Initializes this container from a Dockerfile build."
  @spec build(t(), Dagger.Directory.t(), [
          {:dockerfile, String.t() | nil},
          {:target, String.t() | nil},
          {:build_args, [Dagger.BuildArg.t()]},
          {:secrets, [Dagger.SecretID.t()]}
        ]) :: Dagger.Container.t()
  def build(%__MODULE__{} = container, context, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("build")
      |> QB.put_arg("context", Dagger.ID.id!(context))
      |> QB.maybe_put_arg("dockerfile", optional_args[:dockerfile])
      |> QB.maybe_put_arg("target", optional_args[:target])
      |> QB.maybe_put_arg("buildArgs", optional_args[:build_args])
      |> QB.maybe_put_arg(
        "secrets",
        if(optional_args[:secrets],
          do: Enum.map(optional_args[:secrets], &Dagger.ID.id!/1),
          else: nil
        )
      )

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves default arguments for future commands."
  @spec default_args(t()) :: {:ok, [String.t()]} | {:error, term()}
  def default_args(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("defaultArgs")

    Client.execute(container.client, query_builder)
  end

  @doc """
  Retrieves a directory at the given path.

  Mounts are included.
  """
  @spec directory(t(), String.t()) :: Dagger.Directory.t()
  def directory(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("directory") |> QB.put_arg("path", path)

    %Dagger.Directory{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves entrypoint to be prepended to the arguments of all commands."
  @spec entrypoint(t()) :: {:ok, [String.t()]} | {:error, term()}
  def entrypoint(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("entrypoint")

    Client.execute(container.client, query_builder)
  end

  @doc "Retrieves the value of the specified environment variable."
  @spec env_variable(t(), String.t()) :: {:ok, String.t() | nil} | {:error, term()}
  def env_variable(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("envVariable") |> QB.put_arg("name", name)

    Client.execute(container.client, query_builder)
  end

  @doc "Retrieves the list of environment variables passed to commands."
  @spec env_variables(t()) :: {:ok, [Dagger.EnvVariable.t()]} | {:error, term()}
  def env_variables(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("envVariables") |> QB.select("id")

    with {:ok, items} <- Client.execute(container.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.EnvVariable{
           query_builder:
             QB.query()
             |> QB.select("loadEnvVariableFromID")
             |> QB.put_arg("id", id),
           client: container.client
         }
       end}
    end
  end

  @doc """
  EXPERIMENTAL API! Subject to change/removal at any time.

  Configures all available GPUs on the host to be accessible to this container.

  This currently works for Nvidia devices only.
  """
  @spec experimental_with_all_gpus(t()) :: Dagger.Container.t()
  def experimental_with_all_gpus(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("experimentalWithAllGPUs")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  EXPERIMENTAL API! Subject to change/removal at any time.

  Configures the provided list of devices to be accessible to this container.

  This currently works for Nvidia devices only.
  """
  @spec experimental_with_gpu(t(), [String.t()]) :: Dagger.Container.t()
  def experimental_with_gpu(%__MODULE__{} = container, devices) do
    query_builder =
      container.query_builder
      |> QB.select("experimentalWithGPU")
      |> QB.put_arg("devices", devices)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Writes the container as an OCI tarball to the destination file path on the host.

  It can also export platform variants.
  """
  @spec export(t(), String.t(), [
          {:platform_variants, [Dagger.ContainerID.t()]},
          {:forced_compression, Dagger.ImageLayerCompression.t() | nil},
          {:media_types, Dagger.ImageMediaTypes.t() | nil}
        ]) :: {:ok, String.t()} | {:error, term()}
  def export(%__MODULE__{} = container, path, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("export")
      |> QB.put_arg("path", path)
      |> QB.maybe_put_arg(
        "platformVariants",
        if(optional_args[:platform_variants],
          do: Enum.map(optional_args[:platform_variants], &Dagger.ID.id!/1),
          else: nil
        )
      )
      |> QB.maybe_put_arg("forcedCompression", optional_args[:forced_compression])
      |> QB.maybe_put_arg("mediaTypes", optional_args[:media_types])

    Client.execute(container.client, query_builder)
  end

  @doc """
  Retrieves the list of exposed ports.

  This includes ports already exposed by the image, even if not explicitly added with dagger.
  """
  @spec exposed_ports(t()) :: {:ok, [Dagger.Port.t()]} | {:error, term()}
  def exposed_ports(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("exposedPorts") |> QB.select("id")

    with {:ok, items} <- Client.execute(container.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.Port{
           query_builder:
             QB.query()
             |> QB.select("loadPortFromID")
             |> QB.put_arg("id", id),
           client: container.client
         }
       end}
    end
  end

  @doc """
  Retrieves a file at the given path.

  Mounts are included.
  """
  @spec file(t(), String.t()) :: Dagger.File.t()
  def file(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("file") |> QB.put_arg("path", path)

    %Dagger.File{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Initializes this container from a pulled base image."
  @spec from(t(), String.t()) :: Dagger.Container.t()
  def from(%__MODULE__{} = container, address) do
    query_builder =
      container.query_builder |> QB.select("from") |> QB.put_arg("address", address)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "A unique identifier for this Container."
  @spec id(t()) :: {:ok, Dagger.ContainerID.t()} | {:error, term()}
  def id(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("id")

    Client.execute(container.client, query_builder)
  end

  @doc "The unique image reference which can only be retrieved immediately after the 'Container.From' call."
  @spec image_ref(t()) :: {:ok, String.t()} | {:error, term()}
  def image_ref(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("imageRef")

    Client.execute(container.client, query_builder)
  end

  @doc "Reads the container from an OCI tarball."
  @spec import(t(), Dagger.File.t(), [{:tag, String.t() | nil}]) :: Dagger.Container.t()
  def import(%__MODULE__{} = container, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("import")
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("tag", optional_args[:tag])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves the value of the specified label."
  @spec label(t(), String.t()) :: {:ok, String.t() | nil} | {:error, term()}
  def label(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("label") |> QB.put_arg("name", name)

    Client.execute(container.client, query_builder)
  end

  @doc "Retrieves the list of labels passed to container."
  @spec labels(t()) :: {:ok, [Dagger.Label.t()]} | {:error, term()}
  def labels(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("labels") |> QB.select("id")

    with {:ok, items} <- Client.execute(container.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.Label{
           query_builder:
             QB.query()
             |> QB.select("loadLabelFromID")
             |> QB.put_arg("id", id),
           client: container.client
         }
       end}
    end
  end

  @doc "Retrieves the list of paths where a directory is mounted."
  @spec mounts(t()) :: {:ok, [String.t()]} | {:error, term()}
  def mounts(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("mounts")

    Client.execute(container.client, query_builder)
  end

  @deprecated "Explicit pipeline creation is now a no-op"
  @doc "Creates a named sub-pipeline."
  @spec pipeline(t(), String.t(), [
          {:description, String.t() | nil},
          {:labels, [Dagger.PipelineLabel.t()]}
        ]) :: Dagger.Container.t()
  def pipeline(%__MODULE__{} = container, name, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("pipeline")
      |> QB.put_arg("name", name)
      |> QB.maybe_put_arg("description", optional_args[:description])
      |> QB.maybe_put_arg("labels", optional_args[:labels])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "The platform this container executes and publishes as."
  @spec platform(t()) :: {:ok, Dagger.Platform.t()} | {:error, term()}
  def platform(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("platform")

    Client.execute(container.client, query_builder)
  end

  @doc """
  Publishes this container as a new image to the specified address.

  Publish returns a fully qualified ref.

  It can also publish platform variants.
  """
  @spec publish(t(), String.t(), [
          {:platform_variants, [Dagger.ContainerID.t()]},
          {:forced_compression, Dagger.ImageLayerCompression.t() | nil},
          {:media_types, Dagger.ImageMediaTypes.t() | nil}
        ]) :: {:ok, String.t()} | {:error, term()}
  def publish(%__MODULE__{} = container, address, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("publish")
      |> QB.put_arg("address", address)
      |> QB.maybe_put_arg(
        "platformVariants",
        if(optional_args[:platform_variants],
          do: Enum.map(optional_args[:platform_variants], &Dagger.ID.id!/1),
          else: nil
        )
      )
      |> QB.maybe_put_arg("forcedCompression", optional_args[:forced_compression])
      |> QB.maybe_put_arg("mediaTypes", optional_args[:media_types])

    Client.execute(container.client, query_builder)
  end

  @doc "Retrieves this container's root filesystem. Mounts are not included."
  @spec rootfs(t()) :: Dagger.Directory.t()
  def rootfs(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("rootfs")

    %Dagger.Directory{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  The error stream of the last executed command.

  Will execute default command if none is set, or error if there's no default.
  """
  @spec stderr(t()) :: {:ok, String.t()} | {:error, term()}
  def stderr(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("stderr")

    Client.execute(container.client, query_builder)
  end

  @doc """
  The output stream of the last executed command.

  Will execute default command if none is set, or error if there's no default.
  """
  @spec stdout(t()) :: {:ok, String.t()} | {:error, term()}
  def stdout(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("stdout")

    Client.execute(container.client, query_builder)
  end

  @doc """
  Forces evaluation of the pipeline in the engine.

  It doesn't run the default command if no exec has been set.
  """
  @spec sync(t()) :: {:ok, Dagger.Container.t()} | {:error, term()}
  def sync(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("sync")

    with {:ok, id} <- Client.execute(container.client, query_builder) do
      {:ok,
       %Dagger.Container{
         query_builder:
           QB.query()
           |> QB.select("loadContainerFromID")
           |> QB.put_arg("id", id),
         client: container.client
       }}
    end
  end

  @doc "Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default)."
  @spec terminal(t(), [
          {:cmd, [String.t()]},
          {:experimental_privileged_nesting, boolean() | nil},
          {:insecure_root_capabilities, boolean() | nil}
        ]) :: Dagger.Container.t()
  def terminal(%__MODULE__{} = container, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("terminal")
      |> QB.maybe_put_arg("cmd", optional_args[:cmd])
      |> QB.maybe_put_arg(
        "experimentalPrivilegedNesting",
        optional_args[:experimental_privileged_nesting]
      )
      |> QB.maybe_put_arg("insecureRootCapabilities", optional_args[:insecure_root_capabilities])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves the user to be set for all commands."
  @spec user(t()) :: {:ok, String.t()} | {:error, term()}
  def user(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("user")

    Client.execute(container.client, query_builder)
  end

  @doc "Configures default arguments for future commands."
  @spec with_default_args(t(), [String.t()]) :: Dagger.Container.t()
  def with_default_args(%__MODULE__{} = container, args) do
    query_builder =
      container.query_builder |> QB.select("withDefaultArgs") |> QB.put_arg("args", args)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Set the default command to invoke for the container's terminal API."
  @spec with_default_terminal_cmd(t(), [String.t()], [
          {:experimental_privileged_nesting, boolean() | nil},
          {:insecure_root_capabilities, boolean() | nil}
        ]) :: Dagger.Container.t()
  def with_default_terminal_cmd(%__MODULE__{} = container, args, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withDefaultTerminalCmd")
      |> QB.put_arg("args", args)
      |> QB.maybe_put_arg(
        "experimentalPrivilegedNesting",
        optional_args[:experimental_privileged_nesting]
      )
      |> QB.maybe_put_arg("insecureRootCapabilities", optional_args[:insecure_root_capabilities])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a directory written at the given path."
  @spec with_directory(t(), String.t(), Dagger.Directory.t(), [
          {:exclude, [String.t()]},
          {:include, [String.t()]},
          {:owner, String.t() | nil}
        ]) :: Dagger.Container.t()
  def with_directory(%__MODULE__{} = container, path, directory, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withDirectory")
      |> QB.put_arg("path", path)
      |> QB.put_arg("directory", Dagger.ID.id!(directory))
      |> QB.maybe_put_arg("exclude", optional_args[:exclude])
      |> QB.maybe_put_arg("include", optional_args[:include])
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container but with a different command entrypoint."
  @spec with_entrypoint(t(), [String.t()], [{:keep_default_args, boolean() | nil}]) ::
          Dagger.Container.t()
  def with_entrypoint(%__MODULE__{} = container, args, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withEntrypoint")
      |> QB.put_arg("args", args)
      |> QB.maybe_put_arg("keepDefaultArgs", optional_args[:keep_default_args])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus the given environment variable."
  @spec with_env_variable(t(), String.t(), String.t(), [{:expand, boolean() | nil}]) ::
          Dagger.Container.t()
  def with_env_variable(%__MODULE__{} = container, name, value, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withEnvVariable")
      |> QB.put_arg("name", name)
      |> QB.put_arg("value", value)
      |> QB.maybe_put_arg("expand", optional_args[:expand])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container after executing the specified command inside it."
  @spec with_exec(t(), [String.t()], [
          {:use_entrypoint, boolean() | nil},
          {:stdin, String.t() | nil},
          {:redirect_stdout, String.t() | nil},
          {:redirect_stderr, String.t() | nil},
          {:experimental_privileged_nesting, boolean() | nil},
          {:insecure_root_capabilities, boolean() | nil}
        ]) :: Dagger.Container.t()
  def with_exec(%__MODULE__{} = container, args, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withExec")
      |> QB.put_arg("args", args)
      |> QB.maybe_put_arg("useEntrypoint", optional_args[:use_entrypoint])
      |> QB.maybe_put_arg("stdin", optional_args[:stdin])
      |> QB.maybe_put_arg("redirectStdout", optional_args[:redirect_stdout])
      |> QB.maybe_put_arg("redirectStderr", optional_args[:redirect_stderr])
      |> QB.maybe_put_arg(
        "experimentalPrivilegedNesting",
        optional_args[:experimental_privileged_nesting]
      )
      |> QB.maybe_put_arg("insecureRootCapabilities", optional_args[:insecure_root_capabilities])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Expose a network port.

  Exposed ports serve two purposes:

  - For health checks and introspection, when running services

  - For setting the EXPOSE OCI field when publishing the container
  """
  @spec with_exposed_port(t(), integer(), [
          {:protocol, Dagger.NetworkProtocol.t() | nil},
          {:description, String.t() | nil},
          {:experimental_skip_healthcheck, boolean() | nil}
        ]) :: Dagger.Container.t()
  def with_exposed_port(%__MODULE__{} = container, port, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withExposedPort")
      |> QB.put_arg("port", port)
      |> QB.maybe_put_arg("protocol", optional_args[:protocol])
      |> QB.maybe_put_arg("description", optional_args[:description])
      |> QB.maybe_put_arg(
        "experimentalSkipHealthcheck",
        optional_args[:experimental_skip_healthcheck]
      )

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus the contents of the given file copied to the given path."
  @spec with_file(t(), String.t(), Dagger.File.t(), [
          {:permissions, integer() | nil},
          {:owner, String.t() | nil}
        ]) :: Dagger.Container.t()
  def with_file(%__MODULE__{} = container, path, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withFile")
      |> QB.put_arg("path", path)
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("permissions", optional_args[:permissions])
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus the contents of the given files copied to the given path."
  @spec with_files(t(), String.t(), [Dagger.FileID.t()], [
          {:permissions, integer() | nil},
          {:owner, String.t() | nil}
        ]) :: Dagger.Container.t()
  def with_files(%__MODULE__{} = container, path, sources, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withFiles")
      |> QB.put_arg("path", path)
      |> QB.put_arg("sources", sources)
      |> QB.maybe_put_arg("permissions", optional_args[:permissions])
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Indicate that subsequent operations should be featured more prominently in the UI."
  @spec with_focus(t()) :: Dagger.Container.t()
  def with_focus(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("withFocus")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus the given label."
  @spec with_label(t(), String.t(), String.t()) :: Dagger.Container.t()
  def with_label(%__MODULE__{} = container, name, value) do
    query_builder =
      container.query_builder
      |> QB.select("withLabel")
      |> QB.put_arg("name", name)
      |> QB.put_arg("value", value)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a cache volume mounted at the given path."
  @spec with_mounted_cache(t(), String.t(), Dagger.CacheVolume.t(), [
          {:source, Dagger.DirectoryID.t() | nil},
          {:sharing, Dagger.CacheSharingMode.t() | nil},
          {:owner, String.t() | nil}
        ]) :: Dagger.Container.t()
  def with_mounted_cache(%__MODULE__{} = container, path, cache, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withMountedCache")
      |> QB.put_arg("path", path)
      |> QB.put_arg("cache", Dagger.ID.id!(cache))
      |> QB.maybe_put_arg("source", optional_args[:source])
      |> QB.maybe_put_arg("sharing", optional_args[:sharing])
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a directory mounted at the given path."
  @spec with_mounted_directory(t(), String.t(), Dagger.Directory.t(), [{:owner, String.t() | nil}]) ::
          Dagger.Container.t()
  def with_mounted_directory(%__MODULE__{} = container, path, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withMountedDirectory")
      |> QB.put_arg("path", path)
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a file mounted at the given path."
  @spec with_mounted_file(t(), String.t(), Dagger.File.t(), [{:owner, String.t() | nil}]) ::
          Dagger.Container.t()
  def with_mounted_file(%__MODULE__{} = container, path, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withMountedFile")
      |> QB.put_arg("path", path)
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a secret mounted into a file at the given path."
  @spec with_mounted_secret(t(), String.t(), Dagger.Secret.t(), [
          {:owner, String.t() | nil},
          {:mode, integer() | nil}
        ]) :: Dagger.Container.t()
  def with_mounted_secret(%__MODULE__{} = container, path, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withMountedSecret")
      |> QB.put_arg("path", path)
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("owner", optional_args[:owner])
      |> QB.maybe_put_arg("mode", optional_args[:mode])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs."
  @spec with_mounted_temp(t(), String.t()) :: Dagger.Container.t()
  def with_mounted_temp(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withMountedTemp") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a new file written at the given path."
  @spec with_new_file(t(), String.t(), String.t(), [
          {:permissions, integer() | nil},
          {:owner, String.t() | nil}
        ]) :: Dagger.Container.t()
  def with_new_file(%__MODULE__{} = container, path, contents, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withNewFile")
      |> QB.put_arg("path", path)
      |> QB.put_arg("contents", contents)
      |> QB.maybe_put_arg("permissions", optional_args[:permissions])
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with a registry authentication for a given address."
  @spec with_registry_auth(t(), String.t(), String.t(), Dagger.Secret.t()) :: Dagger.Container.t()
  def with_registry_auth(%__MODULE__{} = container, address, username, secret) do
    query_builder =
      container.query_builder
      |> QB.select("withRegistryAuth")
      |> QB.put_arg("address", address)
      |> QB.put_arg("username", username)
      |> QB.put_arg("secret", Dagger.ID.id!(secret))

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves the container with the given directory mounted to /."
  @spec with_rootfs(t(), Dagger.Directory.t()) :: Dagger.Container.t()
  def with_rootfs(%__MODULE__{} = container, directory) do
    query_builder =
      container.query_builder
      |> QB.select("withRootfs")
      |> QB.put_arg("directory", Dagger.ID.id!(directory))

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus an env variable containing the given secret."
  @spec with_secret_variable(t(), String.t(), Dagger.Secret.t()) :: Dagger.Container.t()
  def with_secret_variable(%__MODULE__{} = container, name, secret) do
    query_builder =
      container.query_builder
      |> QB.select("withSecretVariable")
      |> QB.put_arg("name", name)
      |> QB.put_arg("secret", Dagger.ID.id!(secret))

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Establish a runtime dependency on a service.

  The service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.

  The service will be reachable from the container via the provided hostname alias.

  The service dependency will also convey to any files or directories produced by the container.
  """
  @spec with_service_binding(t(), String.t(), Dagger.Service.t()) :: Dagger.Container.t()
  def with_service_binding(%__MODULE__{} = container, alias, service) do
    query_builder =
      container.query_builder
      |> QB.select("withServiceBinding")
      |> QB.put_arg("alias", alias)
      |> QB.put_arg("service", Dagger.ID.id!(service))

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container plus a socket forwarded to the given Unix socket path."
  @spec with_unix_socket(t(), String.t(), Dagger.Socket.t(), [{:owner, String.t() | nil}]) ::
          Dagger.Container.t()
  def with_unix_socket(%__MODULE__{} = container, path, source, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withUnixSocket")
      |> QB.put_arg("path", path)
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("owner", optional_args[:owner])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with a different command user."
  @spec with_user(t(), String.t()) :: Dagger.Container.t()
  def with_user(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("withUser") |> QB.put_arg("name", name)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with a different working directory."
  @spec with_workdir(t(), String.t()) :: Dagger.Container.t()
  def with_workdir(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withWorkdir") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with unset default arguments for future commands."
  @spec without_default_args(t()) :: Dagger.Container.t()
  def without_default_args(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("withoutDefaultArgs")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with the directory at the given path removed."
  @spec without_directory(t(), String.t()) :: Dagger.Container.t()
  def without_directory(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withoutDirectory") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with an unset command entrypoint."
  @spec without_entrypoint(t(), [{:keep_default_args, boolean() | nil}]) :: Dagger.Container.t()
  def without_entrypoint(%__MODULE__{} = container, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withoutEntrypoint")
      |> QB.maybe_put_arg("keepDefaultArgs", optional_args[:keep_default_args])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container minus the given environment variable."
  @spec without_env_variable(t(), String.t()) :: Dagger.Container.t()
  def without_env_variable(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("withoutEnvVariable") |> QB.put_arg("name", name)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Unexpose a previously exposed port."
  @spec without_exposed_port(t(), integer(), [{:protocol, Dagger.NetworkProtocol.t() | nil}]) ::
          Dagger.Container.t()
  def without_exposed_port(%__MODULE__{} = container, port, optional_args \\ []) do
    query_builder =
      container.query_builder
      |> QB.select("withoutExposedPort")
      |> QB.put_arg("port", port)
      |> QB.maybe_put_arg("protocol", optional_args[:protocol])

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with the file at the given path removed."
  @spec without_file(t(), String.t()) :: Dagger.Container.t()
  def without_file(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withoutFile") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Indicate that subsequent operations should not be featured more prominently in the UI.

  This is the initial state of all containers.
  """
  @spec without_focus(t()) :: Dagger.Container.t()
  def without_focus(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("withoutFocus")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container minus the given environment label."
  @spec without_label(t(), String.t()) :: Dagger.Container.t()
  def without_label(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("withoutLabel") |> QB.put_arg("name", name)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container after unmounting everything at the given path."
  @spec without_mount(t(), String.t()) :: Dagger.Container.t()
  def without_mount(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withoutMount") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container without the registry authentication of a given address."
  @spec without_registry_auth(t(), String.t()) :: Dagger.Container.t()
  def without_registry_auth(%__MODULE__{} = container, address) do
    query_builder =
      container.query_builder
      |> QB.select("withoutRegistryAuth")
      |> QB.put_arg("address", address)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container minus the given environment variable containing the secret."
  @spec without_secret_variable(t(), String.t()) :: Dagger.Container.t()
  def without_secret_variable(%__MODULE__{} = container, name) do
    query_builder =
      container.query_builder |> QB.select("withoutSecretVariable") |> QB.put_arg("name", name)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves this container with a previously added Unix socket removed."
  @spec without_unix_socket(t(), String.t()) :: Dagger.Container.t()
  def without_unix_socket(%__MODULE__{} = container, path) do
    query_builder =
      container.query_builder |> QB.select("withoutUnixSocket") |> QB.put_arg("path", path)

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Retrieves this container with an unset command user.

  Should default to root.
  """
  @spec without_user(t()) :: Dagger.Container.t()
  def without_user(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("withoutUser")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc """
  Retrieves this container with an unset working directory.

  Should default to "/".
  """
  @spec without_workdir(t()) :: Dagger.Container.t()
  def without_workdir(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("withoutWorkdir")

    %Dagger.Container{
      query_builder: query_builder,
      client: container.client
    }
  end

  @doc "Retrieves the working directory for all commands."
  @spec workdir(t()) :: {:ok, String.t()} | {:error, term()}
  def workdir(%__MODULE__{} = container) do
    query_builder =
      container.query_builder |> QB.select("workdir")

    Client.execute(container.client, query_builder)
  end
end
