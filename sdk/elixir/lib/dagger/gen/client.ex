# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Client do
  @moduledoc "The root of the DAG."
  use Dagger.Core.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "Retrieves a content-addressed blob.\n\n## Required Arguments\n\n* `digest` - Digest of the blob\n* `size` - Size of the blob\n* `media_type` - Media type of the blob\n* `uncompressed` - Digest of the uncompressed blob"
    @spec blob(t(), Dagger.String.t(), Dagger.Int.t(), Dagger.String.t(), Dagger.String.t()) ::
            Dagger.Directory.t()
    def blob(%__MODULE__{} = query, digest, size, media_type, uncompressed) do
      selection = select(query.selection, "blob")
      selection = arg(selection, "digest", digest)
      selection = arg(selection, "size", size)
      selection = arg(selection, "mediaType", media_type)
      selection = arg(selection, "uncompressed", uncompressed)
      %Dagger.Directory{selection: selection, client: query.client}
    end
  )

  (
    @doc "Constructs a cache volume for a given cache key.\n\n## Required Arguments\n\n* `key` - A string identifier to target this cache volume (e.g., \"modules-cache\")."
    @spec cache_volume(t(), Dagger.String.t()) :: Dagger.CacheVolume.t()
    def cache_volume(%__MODULE__{} = query, key) do
      selection = select(query.selection, "cacheVolume")
      selection = arg(selection, "key", key)
      %Dagger.CacheVolume{selection: selection, client: query.client}
    end
  )

  (
    @doc "Checks if the current Dagger Engine is compatible with an SDK's required version.\n\n## Required Arguments\n\n* `version` - Version required by the SDK."
    @spec check_version_compatibility(t(), Dagger.String.t()) ::
            {:ok, Dagger.Boolean.t()} | {:error, term()}
    def check_version_compatibility(%__MODULE__{} = query, version) do
      selection = select(query.selection, "checkVersionCompatibility")
      selection = arg(selection, "version", version)
      execute(selection, query.client)
    end
  )

  (
    @doc "Creates a scratch container.\n\nOptional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.\n\n\n\n## Optional Arguments\n\n* `id` - DEPRECATED: Use `loadContainerFromID` instead.\n* `platform` - Platform to initialize the container with."
    @spec container(t(), keyword()) :: Dagger.Container.t()
    def container(%__MODULE__{} = query, optional_args \\ []) do
      selection = select(query.selection, "container")

      selection =
        if is_nil(optional_args[:id]) do
          selection
        else
          {:ok, id} = Dagger.Container.id(optional_args[:id])
          arg(selection, "id", id)
        end

      selection =
        if is_nil(optional_args[:platform]) do
          selection
        else
          arg(selection, "platform", optional_args[:platform])
        end

      %Dagger.Container{selection: selection, client: query.client}
    end
  )

  (
    @doc "The FunctionCall context that the SDK caller is currently executing in.\n\nIf the caller is not currently executing in a function, this will return an error."
    @spec current_function_call(t()) :: Dagger.FunctionCall.t()
    def current_function_call(%__MODULE__{} = query) do
      selection = select(query.selection, "currentFunctionCall")
      %Dagger.FunctionCall{selection: selection, client: query.client}
    end
  )

  (
    @doc "The module currently being served in the session, if any."
    @spec current_module(t()) :: Dagger.CurrentModule.t()
    def current_module(%__MODULE__{} = query) do
      selection = select(query.selection, "currentModule")
      %Dagger.CurrentModule{selection: selection, client: query.client}
    end
  )

  (
    @doc "The TypeDef representations of the objects currently being served in the session."
    @spec current_type_defs(t()) :: {:ok, [Dagger.TypeDef.t()]} | {:error, term()}
    def current_type_defs(%__MODULE__{} = query) do
      selection = select(query.selection, "currentTypeDefs")

      selection =
        select(
          selection,
          "asInput asInterface asList asObject id kind optional withConstructor withField withFunction withInterface withKind withListOf withObject withOptional"
        )

      with {:ok, data} <- execute(selection, query.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.Core.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadTypeDefFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.TypeDef{selection: elem_selection, client: query.client}
         end)}
      end
    end
  )

  (
    @doc "The default platform of the engine."
    @spec default_platform(t()) :: {:ok, Dagger.Platform.t()} | {:error, term()}
    def default_platform(%__MODULE__{} = query) do
      selection = select(query.selection, "defaultPlatform")
      execute(selection, query.client)
    end
  )

  (
    @doc "Creates an empty directory.\n\n\n\n## Optional Arguments\n\n* `id` - DEPRECATED: Use `loadDirectoryFromID` isntead."
    @spec directory(t(), keyword()) :: Dagger.Directory.t()
    def directory(%__MODULE__{} = query, optional_args \\ []) do
      selection = select(query.selection, "directory")

      selection =
        if is_nil(optional_args[:id]) do
          selection
        else
          {:ok, id} = Dagger.Directory.id(optional_args[:id])
          arg(selection, "id", id)
        end

      %Dagger.Directory{selection: selection, client: query.client}
    end
  )

  (
    @doc "## Required Arguments\n\n* `id` -"
    @deprecated "Use `load_file_from_id` instead"
    @spec file(t(), Dagger.FileID.t()) :: Dagger.File.t()
    def file(%__MODULE__{} = query, file) do
      selection = select(query.selection, "file")
      selection = arg(selection, "id", file)
      %Dagger.File{selection: selection, client: query.client}
    end
  )

  (
    @doc "Creates a function.\n\n## Required Arguments\n\n* `name` - Name of the function, in its original format from the implementation language.\n* `return_type` - Return type of the function."
    @spec function(t(), Dagger.String.t(), Dagger.TypeDef.t()) :: Dagger.Function.t()
    def function(%__MODULE__{} = query, name, return_type) do
      selection = select(query.selection, "function")
      selection = arg(selection, "name", name)

      (
        {:ok, id} = Dagger.TypeDef.id(return_type)
        selection = arg(selection, "returnType", id)
      )

      %Dagger.Function{selection: selection, client: query.client}
    end
  )

  (
    @doc "Create a code generation result, given a directory containing the generated code.\n\n## Required Arguments\n\n* `code` -"
    @spec generated_code(t(), Dagger.Directory.t()) :: Dagger.GeneratedCode.t()
    def generated_code(%__MODULE__{} = query, code) do
      selection = select(query.selection, "generatedCode")

      (
        {:ok, id} = Dagger.Directory.id(code)
        selection = arg(selection, "code", id)
      )

      %Dagger.GeneratedCode{selection: selection, client: query.client}
    end
  )

  (
    @doc "Queries a Git repository.\n\n## Required Arguments\n\n* `url` - URL of the git repository.\n\nCan be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.\n\nSuffix \".git\" is optional.\n\n## Optional Arguments\n\n* `keep_git_dir` - Set to true to keep .git directory.\n* `experimental_service_host` - A service which must be started before the repo is fetched.\n* `ssh_known_hosts` - Set SSH known hosts\n* `ssh_auth_socket` - Set SSH auth socket"
    @spec git(t(), Dagger.String.t(), keyword()) :: Dagger.GitRepository.t()
    def git(%__MODULE__{} = query, url, optional_args \\ []) do
      selection = select(query.selection, "git")
      selection = arg(selection, "url", url)

      selection =
        if is_nil(optional_args[:keep_git_dir]) do
          selection
        else
          arg(selection, "keepGitDir", optional_args[:keep_git_dir])
        end

      selection =
        if is_nil(optional_args[:experimental_service_host]) do
          selection
        else
          {:ok, id} = Dagger.Service.id(optional_args[:experimental_service_host])
          arg(selection, "experimentalServiceHost", id)
        end

      selection =
        if is_nil(optional_args[:ssh_known_hosts]) do
          selection
        else
          arg(selection, "sshKnownHosts", optional_args[:ssh_known_hosts])
        end

      selection =
        if is_nil(optional_args[:ssh_auth_socket]) do
          selection
        else
          {:ok, id} = Dagger.Socket.id(optional_args[:ssh_auth_socket])
          arg(selection, "sshAuthSocket", id)
        end

      %Dagger.GitRepository{selection: selection, client: query.client}
    end
  )

  (
    @doc "Queries the host environment."
    @spec host(t()) :: Dagger.Host.t()
    def host(%__MODULE__{} = query) do
      selection = select(query.selection, "host")
      %Dagger.Host{selection: selection, client: query.client}
    end
  )

  (
    @doc "Returns a file containing an http remote url content.\n\n## Required Arguments\n\n* `url` - HTTP url to get the content from (e.g., \"https://docs.dagger.io\").\n\n## Optional Arguments\n\n* `experimental_service_host` - A service which must be started before the URL is fetched."
    @spec http(t(), Dagger.String.t(), keyword()) :: Dagger.File.t()
    def http(%__MODULE__{} = query, url, optional_args \\ []) do
      selection = select(query.selection, "http")
      selection = arg(selection, "url", url)

      selection =
        if is_nil(optional_args[:experimental_service_host]) do
          selection
        else
          {:ok, id} = Dagger.Service.id(optional_args[:experimental_service_host])
          arg(selection, "experimentalServiceHost", id)
        end

      %Dagger.File{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a CacheVolume from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_cache_volume_from_id(t(), Dagger.CacheVolume.t()) :: Dagger.CacheVolume.t()
    def load_cache_volume_from_id(%__MODULE__{} = query, cache_volume) do
      selection = select(query.selection, "loadCacheVolumeFromID")
      selection = arg(selection, "id", cache_volume)
      %Dagger.CacheVolume{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Container from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_container_from_id(t(), Dagger.Container.t()) :: Dagger.Container.t()
    def load_container_from_id(%__MODULE__{} = query, container) do
      selection = select(query.selection, "loadContainerFromID")
      selection = arg(selection, "id", container)
      %Dagger.Container{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a CurrentModule from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_current_module_from_id(t(), Dagger.CurrentModule.t()) :: Dagger.CurrentModule.t()
    def load_current_module_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadCurrentModuleFromID")
      selection = arg(selection, "id", id)
      %Dagger.CurrentModule{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Directory from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_directory_from_id(t(), Dagger.Directory.t()) :: Dagger.Directory.t()
    def load_directory_from_id(%__MODULE__{} = query, directory) do
      selection = select(query.selection, "loadDirectoryFromID")
      selection = arg(selection, "id", directory)
      %Dagger.Directory{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a EnvVariable from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_env_variable_from_id(t(), Dagger.EnvVariable.t()) :: Dagger.EnvVariable.t()
    def load_env_variable_from_id(%__MODULE__{} = query, env_variable) do
      selection = select(query.selection, "loadEnvVariableFromID")
      selection = arg(selection, "id", env_variable)
      %Dagger.EnvVariable{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a FieldTypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_field_type_def_from_id(t(), Dagger.FieldTypeDef.t()) :: Dagger.FieldTypeDef.t()
    def load_field_type_def_from_id(%__MODULE__{} = query, field_type_def) do
      selection = select(query.selection, "loadFieldTypeDefFromID")
      selection = arg(selection, "id", field_type_def)
      %Dagger.FieldTypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a File from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_file_from_id(t(), Dagger.File.t()) :: Dagger.File.t()
    def load_file_from_id(%__MODULE__{} = query, file) do
      selection = select(query.selection, "loadFileFromID")
      selection = arg(selection, "id", file)
      %Dagger.File{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a FunctionArg from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_function_arg_from_id(t(), Dagger.FunctionArg.t()) :: Dagger.FunctionArg.t()
    def load_function_arg_from_id(%__MODULE__{} = query, function_arg) do
      selection = select(query.selection, "loadFunctionArgFromID")
      selection = arg(selection, "id", function_arg)
      %Dagger.FunctionArg{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a FunctionCallArgValue from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_function_call_arg_value_from_id(t(), Dagger.FunctionCallArgValue.t()) ::
            Dagger.FunctionCallArgValue.t()
    def load_function_call_arg_value_from_id(%__MODULE__{} = query, function_call_arg_value) do
      selection = select(query.selection, "loadFunctionCallArgValueFromID")
      selection = arg(selection, "id", function_call_arg_value)
      %Dagger.FunctionCallArgValue{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a FunctionCall from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_function_call_from_id(t(), Dagger.FunctionCall.t()) :: Dagger.FunctionCall.t()
    def load_function_call_from_id(%__MODULE__{} = query, function_call) do
      selection = select(query.selection, "loadFunctionCallFromID")
      selection = arg(selection, "id", function_call)
      %Dagger.FunctionCall{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Function from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_function_from_id(t(), Dagger.Function.t()) :: Dagger.Function.t()
    def load_function_from_id(%__MODULE__{} = query, function) do
      selection = select(query.selection, "loadFunctionFromID")
      selection = arg(selection, "id", function)
      %Dagger.Function{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a GeneratedCode from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_generated_code_from_id(t(), Dagger.GeneratedCode.t()) :: Dagger.GeneratedCode.t()
    def load_generated_code_from_id(%__MODULE__{} = query, generated_code) do
      selection = select(query.selection, "loadGeneratedCodeFromID")
      selection = arg(selection, "id", generated_code)
      %Dagger.GeneratedCode{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a GitModuleSource from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_git_module_source_from_id(t(), Dagger.GitModuleSource.t()) ::
            Dagger.GitModuleSource.t()
    def load_git_module_source_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadGitModuleSourceFromID")
      selection = arg(selection, "id", id)
      %Dagger.GitModuleSource{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a GitRef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_git_ref_from_id(t(), Dagger.GitRef.t()) :: Dagger.GitRef.t()
    def load_git_ref_from_id(%__MODULE__{} = query, git_ref) do
      selection = select(query.selection, "loadGitRefFromID")
      selection = arg(selection, "id", git_ref)
      %Dagger.GitRef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a GitRepository from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_git_repository_from_id(t(), Dagger.GitRepository.t()) :: Dagger.GitRepository.t()
    def load_git_repository_from_id(%__MODULE__{} = query, git_repository) do
      selection = select(query.selection, "loadGitRepositoryFromID")
      selection = arg(selection, "id", git_repository)
      %Dagger.GitRepository{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Host from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_host_from_id(t(), Dagger.Host.t()) :: Dagger.Host.t()
    def load_host_from_id(%__MODULE__{} = query, host) do
      selection = select(query.selection, "loadHostFromID")
      selection = arg(selection, "id", host)
      %Dagger.Host{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a InputTypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_input_type_def_from_id(t(), Dagger.InputTypeDef.t()) :: Dagger.InputTypeDef.t()
    def load_input_type_def_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadInputTypeDefFromID")
      selection = arg(selection, "id", id)
      %Dagger.InputTypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a InterfaceTypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_interface_type_def_from_id(t(), Dagger.InterfaceTypeDef.t()) ::
            Dagger.InterfaceTypeDef.t()
    def load_interface_type_def_from_id(%__MODULE__{} = query, interface_type_def) do
      selection = select(query.selection, "loadInterfaceTypeDefFromID")
      selection = arg(selection, "id", interface_type_def)
      %Dagger.InterfaceTypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Label from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_label_from_id(t(), Dagger.Label.t()) :: Dagger.Label.t()
    def load_label_from_id(%__MODULE__{} = query, label) do
      selection = select(query.selection, "loadLabelFromID")
      selection = arg(selection, "id", label)
      %Dagger.Label{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a ListTypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_list_type_def_from_id(t(), Dagger.ListTypeDef.t()) :: Dagger.ListTypeDef.t()
    def load_list_type_def_from_id(%__MODULE__{} = query, list_type_def) do
      selection = select(query.selection, "loadListTypeDefFromID")
      selection = arg(selection, "id", list_type_def)
      %Dagger.ListTypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a LocalModuleSource from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_local_module_source_from_id(t(), Dagger.LocalModuleSource.t()) ::
            Dagger.LocalModuleSource.t()
    def load_local_module_source_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadLocalModuleSourceFromID")
      selection = arg(selection, "id", id)
      %Dagger.LocalModuleSource{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a ModuleDependency from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_module_dependency_from_id(t(), Dagger.ModuleDependency.t()) ::
            Dagger.ModuleDependency.t()
    def load_module_dependency_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadModuleDependencyFromID")
      selection = arg(selection, "id", id)
      %Dagger.ModuleDependency{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Module from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_module_from_id(t(), Dagger.Module.t()) :: Dagger.Module.t()
    def load_module_from_id(%__MODULE__{} = query, module) do
      selection = select(query.selection, "loadModuleFromID")
      selection = arg(selection, "id", module)
      %Dagger.Module{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a ModuleSource from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_module_source_from_id(t(), Dagger.ModuleSource.t()) :: Dagger.ModuleSource.t()
    def load_module_source_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadModuleSourceFromID")
      selection = arg(selection, "id", id)
      %Dagger.ModuleSource{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a ObjectTypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_object_type_def_from_id(t(), Dagger.ObjectTypeDef.t()) :: Dagger.ObjectTypeDef.t()
    def load_object_type_def_from_id(%__MODULE__{} = query, object_type_def) do
      selection = select(query.selection, "loadObjectTypeDefFromID")
      selection = arg(selection, "id", object_type_def)
      %Dagger.ObjectTypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Port from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_port_from_id(t(), Dagger.Port.t()) :: Dagger.Port.t()
    def load_port_from_id(%__MODULE__{} = query, port) do
      selection = select(query.selection, "loadPortFromID")
      selection = arg(selection, "id", port)
      %Dagger.Port{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Secret from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_secret_from_id(t(), Dagger.Secret.t()) :: Dagger.Secret.t()
    def load_secret_from_id(%__MODULE__{} = query, secret) do
      selection = select(query.selection, "loadSecretFromID")
      selection = arg(selection, "id", secret)
      %Dagger.Secret{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Service from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_service_from_id(t(), Dagger.Service.t()) :: Dagger.Service.t()
    def load_service_from_id(%__MODULE__{} = query, service) do
      selection = select(query.selection, "loadServiceFromID")
      selection = arg(selection, "id", service)
      %Dagger.Service{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Socket from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_socket_from_id(t(), Dagger.Socket.t()) :: Dagger.Socket.t()
    def load_socket_from_id(%__MODULE__{} = query, socket) do
      selection = select(query.selection, "loadSocketFromID")
      selection = arg(selection, "id", socket)
      %Dagger.Socket{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a Terminal from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_terminal_from_id(t(), Dagger.Terminal.t()) :: Dagger.Terminal.t()
    def load_terminal_from_id(%__MODULE__{} = query, id) do
      selection = select(query.selection, "loadTerminalFromID")
      selection = arg(selection, "id", id)
      %Dagger.Terminal{selection: selection, client: query.client}
    end
  )

  (
    @doc "Load a TypeDef from its ID.\n\n## Required Arguments\n\n* `id` -"
    @spec load_type_def_from_id(t(), Dagger.TypeDef.t()) :: Dagger.TypeDef.t()
    def load_type_def_from_id(%__MODULE__{} = query, type_def) do
      selection = select(query.selection, "loadTypeDefFromID")
      selection = arg(selection, "id", type_def)
      %Dagger.TypeDef{selection: selection, client: query.client}
    end
  )

  (
    @doc "Create a new module."
    @spec module(t()) :: Dagger.Module.t()
    def module(%__MODULE__{} = query) do
      selection = select(query.selection, "module")
      %Dagger.Module{selection: selection, client: query.client}
    end
  )

  (
    @doc "Create a new module dependency configuration from a module source and name\n\n## Required Arguments\n\n* `source` - The source of the dependency\n\n## Optional Arguments\n\n* `name` - If set, the name to use for the dependency. Otherwise, once installed to a parent module, the name of the dependency module will be used by default."
    @spec module_dependency(t(), Dagger.ModuleSource.t(), keyword()) ::
            Dagger.ModuleDependency.t()
    def module_dependency(%__MODULE__{} = query, source, optional_args \\ []) do
      selection = select(query.selection, "moduleDependency")
      selection = arg(selection, "source", source)

      selection =
        if is_nil(optional_args[:name]) do
          selection
        else
          arg(selection, "name", optional_args[:name])
        end

      %Dagger.ModuleDependency{selection: selection, client: query.client}
    end
  )

  (
    @doc "Create a new module source instance from a source ref string.\n\n## Required Arguments\n\n* `ref_string` - The string ref representation of the module source\n\n## Optional Arguments\n\n* `root_directory` - An explicitly set root directory for the module source. This is required to load local sources as modules; other source types implicitly encode the root directory and do not require this.\n* `stable` - If true, enforce that the source is a stable version for source kinds that support versioning."
    @spec module_source(t(), Dagger.String.t(), keyword()) :: Dagger.ModuleSource.t()
    def module_source(%__MODULE__{} = query, ref_string, optional_args \\ []) do
      selection = select(query.selection, "moduleSource")
      selection = arg(selection, "refString", ref_string)

      selection =
        if is_nil(optional_args[:root_directory]) do
          selection
        else
          {:ok, id} = Dagger.Directory.id(optional_args[:root_directory])
          arg(selection, "rootDirectory", id)
        end

      selection =
        if is_nil(optional_args[:stable]) do
          selection
        else
          arg(selection, "stable", optional_args[:stable])
        end

      %Dagger.ModuleSource{selection: selection, client: query.client}
    end
  )

  (
    @doc "Creates a named sub-pipeline.\n\n## Required Arguments\n\n* `name` - Name of the sub-pipeline.\n\n## Optional Arguments\n\n* `description` - Description of the sub-pipeline.\n* `labels` - Labels to apply to the sub-pipeline."
    @spec pipeline(t(), Dagger.String.t(), keyword()) :: Dagger.Client.t()
    def pipeline(%__MODULE__{} = query, name, optional_args \\ []) do
      selection = select(query.selection, "pipeline")
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

      %Dagger.Client{selection: selection, client: query.client}
    end
  )

  (
    @doc "Reference a secret by name.\n\n## Required Arguments\n\n* `name` -"
    @spec secret(t(), Dagger.String.t()) :: Dagger.Secret.t()
    def secret(%__MODULE__{} = query, name) do
      selection = select(query.selection, "secret")
      selection = arg(selection, "name", name)
      %Dagger.Secret{selection: selection, client: query.client}
    end
  )

  (
    @doc "Sets a secret given a user defined name to its plaintext and returns the secret.\n\nThe plaintext value is limited to a size of 128000 bytes.\n\n## Required Arguments\n\n* `name` - The user defined name for this secret\n* `plaintext` - The plaintext of the secret"
    @spec set_secret(t(), Dagger.String.t(), Dagger.String.t()) :: Dagger.Secret.t()
    def set_secret(%__MODULE__{} = query, name, plaintext) do
      selection = select(query.selection, "setSecret")
      selection = arg(selection, "name", name)
      selection = arg(selection, "plaintext", plaintext)
      %Dagger.Secret{selection: selection, client: query.client}
    end
  )

  (
    @doc "Loads a socket by its ID.\n\n## Required Arguments\n\n* `id` -"
    @deprecated "Use `load_socket_from_id` instead"
    @spec socket(t(), Dagger.Socket.t()) :: Dagger.Socket.t()
    def socket(%__MODULE__{} = query, socket) do
      selection = select(query.selection, "socket")

      (
        {:ok, id} = Dagger.Socket.id(socket)
        selection = arg(selection, "id", id)
      )

      %Dagger.Socket{selection: selection, client: query.client}
    end
  )

  (
    @doc "Create a new TypeDef."
    @spec type_def(t()) :: Dagger.TypeDef.t()
    def type_def(%__MODULE__{} = query) do
      selection = select(query.selection, "typeDef")
      %Dagger.TypeDef{selection: selection, client: query.client}
    end
  )
end
