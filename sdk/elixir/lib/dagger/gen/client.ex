# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Client do
  @moduledoc "The root of the DAG."

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "Retrieves a container builtin to the engine."
  @spec builtin_container(t(), String.t()) :: Dagger.Container.t()
  def builtin_container(%__MODULE__{} = client, digest) do
    query_builder =
      client.query_builder |> QB.select("builtinContainer") |> QB.put_arg("digest", digest)

    %Dagger.Container{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Constructs a cache volume for a given cache key."
  @spec cache_volume(t(), String.t(), [{:namespace, String.t() | nil}]) :: Dagger.CacheVolume.t()
  def cache_volume(%__MODULE__{} = client, key, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("cacheVolume")
      |> QB.put_arg("key", key)
      |> QB.maybe_put_arg("namespace", optional_args[:namespace])

    %Dagger.CacheVolume{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc """
  Creates a scratch container.

  Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
  """
  @spec container(t(), [{:platform, Dagger.Platform.t() | nil}]) :: Dagger.Container.t()
  def container(%__MODULE__{} = client, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("container")
      |> QB.maybe_put_arg("platform", optional_args[:platform])

    %Dagger.Container{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc """
  The FunctionCall context that the SDK caller is currently executing in.

  If the caller is not currently executing in a function, this will return an error.
  """
  @spec current_function_call(t()) :: Dagger.FunctionCall.t()
  def current_function_call(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("currentFunctionCall")

    %Dagger.FunctionCall{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "The module currently being served in the session, if any."
  @spec current_module(t()) :: Dagger.CurrentModule.t()
  def current_module(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("currentModule")

    %Dagger.CurrentModule{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "The TypeDef representations of the objects currently being served in the session."
  @spec current_type_defs(t()) :: {:ok, [Dagger.TypeDef.t()]} | {:error, term()}
  def current_type_defs(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("currentTypeDefs") |> QB.select("id")

    with {:ok, items} <- Client.execute(client.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.TypeDef{
           query_builder:
             QB.query()
             |> QB.select("loadTypeDefFromID")
             |> QB.put_arg("id", id),
           client: client.client
         }
       end}
    end
  end

  @doc "The default platform of the engine."
  @spec default_platform(t()) :: {:ok, Dagger.Platform.t()} | {:error, term()}
  def default_platform(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("defaultPlatform")

    Client.execute(client.client, query_builder)
  end

  @doc "Creates an empty directory."
  @spec directory(t()) :: Dagger.Directory.t()
  def directory(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("directory")

    %Dagger.Directory{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "The Dagger engine container configuration and state"
  @spec engine(t()) :: Dagger.Engine.t()
  def engine(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("engine")

    %Dagger.Engine{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new error."
  @spec error(t(), String.t()) :: Dagger.Error.t()
  def error(%__MODULE__{} = client, message) do
    query_builder =
      client.query_builder |> QB.select("error") |> QB.put_arg("message", message)

    %Dagger.Error{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Creates a function."
  @spec function(t(), String.t(), Dagger.TypeDef.t()) :: Dagger.Function.t()
  def function(%__MODULE__{} = client, name, return_type) do
    query_builder =
      client.query_builder
      |> QB.select("function")
      |> QB.put_arg("name", name)
      |> QB.put_arg("returnType", Dagger.ID.id!(return_type))

    %Dagger.Function{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a code generation result, given a directory containing the generated code."
  @spec generated_code(t(), Dagger.Directory.t()) :: Dagger.GeneratedCode.t()
  def generated_code(%__MODULE__{} = client, code) do
    query_builder =
      client.query_builder
      |> QB.select("generatedCode")
      |> QB.put_arg("code", Dagger.ID.id!(code))

    %Dagger.GeneratedCode{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Queries a Git repository."
  @spec git(t(), String.t(), [
          {:keep_git_dir, boolean() | nil},
          {:experimental_service_host, Dagger.ServiceID.t() | nil},
          {:ssh_known_hosts, String.t() | nil},
          {:ssh_auth_socket, Dagger.SocketID.t() | nil}
        ]) :: Dagger.GitRepository.t()
  def git(%__MODULE__{} = client, url, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("git")
      |> QB.put_arg("url", url)
      |> QB.maybe_put_arg("keepGitDir", optional_args[:keep_git_dir])
      |> QB.maybe_put_arg("experimentalServiceHost", optional_args[:experimental_service_host])
      |> QB.maybe_put_arg("sshKnownHosts", optional_args[:ssh_known_hosts])
      |> QB.maybe_put_arg("sshAuthSocket", optional_args[:ssh_auth_socket])

    %Dagger.GitRepository{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Queries the host environment."
  @spec host(t()) :: Dagger.Host.t()
  def host(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("host")

    %Dagger.Host{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Returns a file containing an http remote url content."
  @spec http(t(), String.t(), [{:experimental_service_host, Dagger.ServiceID.t() | nil}]) ::
          Dagger.File.t()
  def http(%__MODULE__{} = client, url, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("http")
      |> QB.put_arg("url", url)
      |> QB.maybe_put_arg("experimentalServiceHost", optional_args[:experimental_service_host])

    %Dagger.File{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a CacheVolume from its ID."
  @spec load_cache_volume_from_id(t(), Dagger.CacheVolumeID.t()) :: Dagger.CacheVolume.t()
  def load_cache_volume_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadCacheVolumeFromID") |> QB.put_arg("id", id)

    %Dagger.CacheVolume{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Container from its ID."
  @spec load_container_from_id(t(), Dagger.ContainerID.t()) :: Dagger.Container.t()
  def load_container_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadContainerFromID") |> QB.put_arg("id", id)

    %Dagger.Container{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a CurrentModule from its ID."
  @spec load_current_module_from_id(t(), Dagger.CurrentModuleID.t()) :: Dagger.CurrentModule.t()
  def load_current_module_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadCurrentModuleFromID") |> QB.put_arg("id", id)

    %Dagger.CurrentModule{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Directory from its ID."
  @spec load_directory_from_id(t(), Dagger.DirectoryID.t()) :: Dagger.Directory.t()
  def load_directory_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadDirectoryFromID") |> QB.put_arg("id", id)

    %Dagger.Directory{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EngineCacheEntry from its ID."
  @spec load_engine_cache_entry_from_id(t(), Dagger.EngineCacheEntryID.t()) ::
          Dagger.EngineCacheEntry.t()
  def load_engine_cache_entry_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEngineCacheEntryFromID") |> QB.put_arg("id", id)

    %Dagger.EngineCacheEntry{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EngineCacheEntrySet from its ID."
  @spec load_engine_cache_entry_set_from_id(t(), Dagger.EngineCacheEntrySetID.t()) ::
          Dagger.EngineCacheEntrySet.t()
  def load_engine_cache_entry_set_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEngineCacheEntrySetFromID") |> QB.put_arg("id", id)

    %Dagger.EngineCacheEntrySet{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EngineCache from its ID."
  @spec load_engine_cache_from_id(t(), Dagger.EngineCacheID.t()) :: Dagger.EngineCache.t()
  def load_engine_cache_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEngineCacheFromID") |> QB.put_arg("id", id)

    %Dagger.EngineCache{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Engine from its ID."
  @spec load_engine_from_id(t(), Dagger.EngineID.t()) :: Dagger.Engine.t()
  def load_engine_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEngineFromID") |> QB.put_arg("id", id)

    %Dagger.Engine{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EnumTypeDef from its ID."
  @spec load_enum_type_def_from_id(t(), Dagger.EnumTypeDefID.t()) :: Dagger.EnumTypeDef.t()
  def load_enum_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEnumTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.EnumTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EnumValueTypeDef from its ID."
  @spec load_enum_value_type_def_from_id(t(), Dagger.EnumValueTypeDefID.t()) ::
          Dagger.EnumValueTypeDef.t()
  def load_enum_value_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEnumValueTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.EnumValueTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a EnvVariable from its ID."
  @spec load_env_variable_from_id(t(), Dagger.EnvVariableID.t()) :: Dagger.EnvVariable.t()
  def load_env_variable_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadEnvVariableFromID") |> QB.put_arg("id", id)

    %Dagger.EnvVariable{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Error from its ID."
  @spec load_error_from_id(t(), Dagger.ErrorID.t()) :: Dagger.Error.t()
  def load_error_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadErrorFromID") |> QB.put_arg("id", id)

    %Dagger.Error{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a FieldTypeDef from its ID."
  @spec load_field_type_def_from_id(t(), Dagger.FieldTypeDefID.t()) :: Dagger.FieldTypeDef.t()
  def load_field_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFieldTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.FieldTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a File from its ID."
  @spec load_file_from_id(t(), Dagger.FileID.t()) :: Dagger.File.t()
  def load_file_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFileFromID") |> QB.put_arg("id", id)

    %Dagger.File{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a FunctionArg from its ID."
  @spec load_function_arg_from_id(t(), Dagger.FunctionArgID.t()) :: Dagger.FunctionArg.t()
  def load_function_arg_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFunctionArgFromID") |> QB.put_arg("id", id)

    %Dagger.FunctionArg{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a FunctionCallArgValue from its ID."
  @spec load_function_call_arg_value_from_id(t(), Dagger.FunctionCallArgValueID.t()) ::
          Dagger.FunctionCallArgValue.t()
  def load_function_call_arg_value_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFunctionCallArgValueFromID") |> QB.put_arg("id", id)

    %Dagger.FunctionCallArgValue{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a FunctionCall from its ID."
  @spec load_function_call_from_id(t(), Dagger.FunctionCallID.t()) :: Dagger.FunctionCall.t()
  def load_function_call_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFunctionCallFromID") |> QB.put_arg("id", id)

    %Dagger.FunctionCall{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Function from its ID."
  @spec load_function_from_id(t(), Dagger.FunctionID.t()) :: Dagger.Function.t()
  def load_function_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadFunctionFromID") |> QB.put_arg("id", id)

    %Dagger.Function{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a GeneratedCode from its ID."
  @spec load_generated_code_from_id(t(), Dagger.GeneratedCodeID.t()) :: Dagger.GeneratedCode.t()
  def load_generated_code_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadGeneratedCodeFromID") |> QB.put_arg("id", id)

    %Dagger.GeneratedCode{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a GitModuleSource from its ID."
  @spec load_git_module_source_from_id(t(), Dagger.GitModuleSourceID.t()) ::
          Dagger.GitModuleSource.t()
  def load_git_module_source_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadGitModuleSourceFromID") |> QB.put_arg("id", id)

    %Dagger.GitModuleSource{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a GitRef from its ID."
  @spec load_git_ref_from_id(t(), Dagger.GitRefID.t()) :: Dagger.GitRef.t()
  def load_git_ref_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadGitRefFromID") |> QB.put_arg("id", id)

    %Dagger.GitRef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a GitRepository from its ID."
  @spec load_git_repository_from_id(t(), Dagger.GitRepositoryID.t()) :: Dagger.GitRepository.t()
  def load_git_repository_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadGitRepositoryFromID") |> QB.put_arg("id", id)

    %Dagger.GitRepository{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Host from its ID."
  @spec load_host_from_id(t(), Dagger.HostID.t()) :: Dagger.Host.t()
  def load_host_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadHostFromID") |> QB.put_arg("id", id)

    %Dagger.Host{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a InputTypeDef from its ID."
  @spec load_input_type_def_from_id(t(), Dagger.InputTypeDefID.t()) :: Dagger.InputTypeDef.t()
  def load_input_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadInputTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.InputTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a InterfaceTypeDef from its ID."
  @spec load_interface_type_def_from_id(t(), Dagger.InterfaceTypeDefID.t()) ::
          Dagger.InterfaceTypeDef.t()
  def load_interface_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadInterfaceTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.InterfaceTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Label from its ID."
  @spec load_label_from_id(t(), Dagger.LabelID.t()) :: Dagger.Label.t()
  def load_label_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadLabelFromID") |> QB.put_arg("id", id)

    %Dagger.Label{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ListTypeDef from its ID."
  @spec load_list_type_def_from_id(t(), Dagger.ListTypeDefID.t()) :: Dagger.ListTypeDef.t()
  def load_list_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadListTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.ListTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a LocalModuleSource from its ID."
  @spec load_local_module_source_from_id(t(), Dagger.LocalModuleSourceID.t()) ::
          Dagger.LocalModuleSource.t()
  def load_local_module_source_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadLocalModuleSourceFromID") |> QB.put_arg("id", id)

    %Dagger.LocalModuleSource{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ModuleDependency from its ID."
  @spec load_module_dependency_from_id(t(), Dagger.ModuleDependencyID.t()) ::
          Dagger.ModuleDependency.t()
  def load_module_dependency_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadModuleDependencyFromID") |> QB.put_arg("id", id)

    %Dagger.ModuleDependency{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Module from its ID."
  @spec load_module_from_id(t(), Dagger.ModuleID.t()) :: Dagger.Module.t()
  def load_module_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadModuleFromID") |> QB.put_arg("id", id)

    %Dagger.Module{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ModuleSource from its ID."
  @spec load_module_source_from_id(t(), Dagger.ModuleSourceID.t()) :: Dagger.ModuleSource.t()
  def load_module_source_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadModuleSourceFromID") |> QB.put_arg("id", id)

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ModuleSourceView from its ID."
  @spec load_module_source_view_from_id(t(), Dagger.ModuleSourceViewID.t()) ::
          Dagger.ModuleSourceView.t()
  def load_module_source_view_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadModuleSourceViewFromID") |> QB.put_arg("id", id)

    %Dagger.ModuleSourceView{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ObjectTypeDef from its ID."
  @spec load_object_type_def_from_id(t(), Dagger.ObjectTypeDefID.t()) :: Dagger.ObjectTypeDef.t()
  def load_object_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadObjectTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.ObjectTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Port from its ID."
  @spec load_port_from_id(t(), Dagger.PortID.t()) :: Dagger.Port.t()
  def load_port_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadPortFromID") |> QB.put_arg("id", id)

    %Dagger.Port{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a SDKConfig from its ID."
  @spec load_sdk_config_from_id(t(), Dagger.SDKConfigID.t()) :: Dagger.SDKConfig.t() | nil
  def load_sdk_config_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSDKConfigFromID") |> QB.put_arg("id", id)

    %Dagger.SDKConfig{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a ScalarTypeDef from its ID."
  @spec load_scalar_type_def_from_id(t(), Dagger.ScalarTypeDefID.t()) :: Dagger.ScalarTypeDef.t()
  def load_scalar_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadScalarTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.ScalarTypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Secret from its ID."
  @spec load_secret_from_id(t(), Dagger.SecretID.t()) :: Dagger.Secret.t()
  def load_secret_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSecretFromID") |> QB.put_arg("id", id)

    %Dagger.Secret{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Secret from its Name."
  @spec load_secret_from_name(t(), String.t(), [{:accessor, String.t() | nil}]) ::
          Dagger.Secret.t()
  def load_secret_from_name(%__MODULE__{} = client, name, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("loadSecretFromName")
      |> QB.put_arg("name", name)
      |> QB.maybe_put_arg("accessor", optional_args[:accessor])

    %Dagger.Secret{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Service from its ID."
  @spec load_service_from_id(t(), Dagger.ServiceID.t()) :: Dagger.Service.t()
  def load_service_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadServiceFromID") |> QB.put_arg("id", id)

    %Dagger.Service{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Socket from its ID."
  @spec load_socket_from_id(t(), Dagger.SocketID.t()) :: Dagger.Socket.t()
  def load_socket_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSocketFromID") |> QB.put_arg("id", id)

    %Dagger.Socket{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a SourceMap from its ID."
  @spec load_source_map_from_id(t(), Dagger.SourceMapID.t()) :: Dagger.SourceMap.t()
  def load_source_map_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSourceMapFromID") |> QB.put_arg("id", id)

    %Dagger.SourceMap{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a SpanContext from its ID."
  @spec load_span_context_from_id(t(), Dagger.SpanContextID.t()) :: Dagger.SpanContext.t()
  def load_span_context_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSpanContextFromID") |> QB.put_arg("id", id)

    %Dagger.SpanContext{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Span from its ID."
  @spec load_span_from_id(t(), Dagger.SpanID.t()) :: Dagger.Span.t()
  def load_span_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadSpanFromID") |> QB.put_arg("id", id)

    %Dagger.Span{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a Terminal from its ID."
  @spec load_terminal_from_id(t(), Dagger.TerminalID.t()) :: Dagger.Terminal.t()
  def load_terminal_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadTerminalFromID") |> QB.put_arg("id", id)

    %Dagger.Terminal{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Load a TypeDef from its ID."
  @spec load_type_def_from_id(t(), Dagger.TypeDefID.t()) :: Dagger.TypeDef.t()
  def load_type_def_from_id(%__MODULE__{} = client, id) do
    query_builder =
      client.query_builder |> QB.select("loadTypeDefFromID") |> QB.put_arg("id", id)

    %Dagger.TypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new module."
  @spec module(t()) :: Dagger.Module.t()
  def module(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("module")

    %Dagger.Module{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new module dependency configuration from a module source and name"
  @spec module_dependency(t(), Dagger.ModuleSource.t(), [{:name, String.t() | nil}]) ::
          Dagger.ModuleDependency.t()
  def module_dependency(%__MODULE__{} = client, source, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("moduleDependency")
      |> QB.put_arg("source", Dagger.ID.id!(source))
      |> QB.maybe_put_arg("name", optional_args[:name])

    %Dagger.ModuleDependency{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new module source instance from a source ref string."
  @spec module_source(t(), String.t(), [
          {:ref_pin, String.t() | nil},
          {:stable, boolean() | nil},
          {:rel_host_path, String.t() | nil}
        ]) :: Dagger.ModuleSource.t()
  def module_source(%__MODULE__{} = client, ref_string, optional_args \\ []) do
    query_builder =
      client.query_builder
      |> QB.select("moduleSource")
      |> QB.put_arg("refString", ref_string)
      |> QB.maybe_put_arg("refPin", optional_args[:ref_pin])
      |> QB.maybe_put_arg("stable", optional_args[:stable])
      |> QB.maybe_put_arg("relHostPath", optional_args[:rel_host_path])

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Creates a new secret."
  @spec secret(t(), String.t()) :: Dagger.Secret.t()
  def secret(%__MODULE__{} = client, uri) do
    query_builder =
      client.query_builder |> QB.select("secret") |> QB.put_arg("uri", uri)

    %Dagger.Secret{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc """
  Sets a secret given a user defined name to its plaintext and returns the secret.

  The plaintext value is limited to a size of 128000 bytes.
  """
  @spec set_secret(t(), String.t(), String.t()) :: Dagger.Secret.t()
  def set_secret(%__MODULE__{} = client, name, plaintext) do
    query_builder =
      client.query_builder
      |> QB.select("setSecret")
      |> QB.put_arg("name", name)
      |> QB.put_arg("plaintext", plaintext)

    %Dagger.Secret{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Creates source map metadata."
  @spec source_map(t(), String.t(), integer(), integer()) :: Dagger.SourceMap.t()
  def source_map(%__MODULE__{} = client, filename, line, column) do
    query_builder =
      client.query_builder
      |> QB.select("sourceMap")
      |> QB.put_arg("filename", filename)
      |> QB.put_arg("line", line)
      |> QB.put_arg("column", column)

    %Dagger.SourceMap{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new OpenTelemetry span."
  @spec span(t(), String.t()) :: Dagger.Span.t()
  def span(%__MODULE__{} = client, name) do
    query_builder =
      client.query_builder |> QB.select("span") |> QB.put_arg("name", name)

    %Dagger.Span{
      query_builder: query_builder,
      client: client.client
    }
  end

  @spec span_context(t()) :: Dagger.SpanContext.t()
  def span_context(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("spanContext")

    %Dagger.SpanContext{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Create a new TypeDef."
  @spec type_def(t()) :: Dagger.TypeDef.t()
  def type_def(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("typeDef")

    %Dagger.TypeDef{
      query_builder: query_builder,
      client: client.client
    }
  end

  @doc "Get the current Dagger Engine version."
  @spec version(t()) :: {:ok, String.t()} | {:error, term()}
  def version(%__MODULE__{} = client) do
    query_builder =
      client.query_builder |> QB.select("version")

    Client.execute(client.client, query_builder)
  end
end
