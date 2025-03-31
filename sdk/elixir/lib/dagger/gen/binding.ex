# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Binding do
  @moduledoc "Dagger.Binding"

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "Retrieve the binding value, as type CacheVolume"
  @spec as_cache_volume(t()) :: Dagger.CacheVolume.t()
  def as_cache_volume(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asCacheVolume")

    %Dagger.CacheVolume{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Container"
  @spec as_container(t()) :: Dagger.Container.t()
  def as_container(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asContainer")

    %Dagger.Container{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type CurrentModule"
  @spec as_current_module(t()) :: Dagger.CurrentModule.t()
  def as_current_module(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asCurrentModule")

    %Dagger.CurrentModule{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Directory"
  @spec as_directory(t()) :: Dagger.Directory.t()
  def as_directory(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asDirectory")

    %Dagger.Directory{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type EnumTypeDef"
  @spec as_enum_type_def(t()) :: Dagger.EnumTypeDef.t()
  def as_enum_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asEnumTypeDef")

    %Dagger.EnumTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type EnumValueTypeDef"
  @spec as_enum_value_type_def(t()) :: Dagger.EnumValueTypeDef.t()
  def as_enum_value_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asEnumValueTypeDef")

    %Dagger.EnumValueTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Environment"
  @spec as_environment(t()) :: Dagger.Environment.t()
  def as_environment(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asEnvironment")

    %Dagger.Environment{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Error"
  @spec as_error(t()) :: Dagger.Error.t()
  def as_error(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asError")

    %Dagger.Error{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ErrorValue"
  @spec as_error_value(t()) :: Dagger.ErrorValue.t()
  def as_error_value(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asErrorValue")

    %Dagger.ErrorValue{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type FieldTypeDef"
  @spec as_field_type_def(t()) :: Dagger.FieldTypeDef.t()
  def as_field_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFieldTypeDef")

    %Dagger.FieldTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type File"
  @spec as_file(t()) :: Dagger.File.t()
  def as_file(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFile")

    %Dagger.File{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Function"
  @spec as_function(t()) :: Dagger.Function.t()
  def as_function(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFunction")

    %Dagger.Function{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type FunctionArg"
  @spec as_function_arg(t()) :: Dagger.FunctionArg.t()
  def as_function_arg(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFunctionArg")

    %Dagger.FunctionArg{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type FunctionCall"
  @spec as_function_call(t()) :: Dagger.FunctionCall.t()
  def as_function_call(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFunctionCall")

    %Dagger.FunctionCall{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type FunctionCallArgValue"
  @spec as_function_call_arg_value(t()) :: Dagger.FunctionCallArgValue.t()
  def as_function_call_arg_value(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asFunctionCallArgValue")

    %Dagger.FunctionCallArgValue{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type GeneratedCode"
  @spec as_generated_code(t()) :: Dagger.GeneratedCode.t()
  def as_generated_code(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asGeneratedCode")

    %Dagger.GeneratedCode{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type GitRef"
  @spec as_git_ref(t()) :: Dagger.GitRef.t()
  def as_git_ref(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asGitRef")

    %Dagger.GitRef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type GitRepository"
  @spec as_git_repository(t()) :: Dagger.GitRepository.t()
  def as_git_repository(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asGitRepository")

    %Dagger.GitRepository{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type InputTypeDef"
  @spec as_input_type_def(t()) :: Dagger.InputTypeDef.t()
  def as_input_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asInputTypeDef")

    %Dagger.InputTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type InterfaceTypeDef"
  @spec as_interface_type_def(t()) :: Dagger.InterfaceTypeDef.t()
  def as_interface_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asInterfaceTypeDef")

    %Dagger.InterfaceTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type LLM"
  @spec as_llm(t()) :: Dagger.LLM.t()
  def as_llm(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asLLM")

    %Dagger.LLM{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type LLMTokenUsage"
  @spec as_llm_token_usage(t()) :: Dagger.LLMTokenUsage.t()
  def as_llm_token_usage(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asLLMTokenUsage")

    %Dagger.LLMTokenUsage{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type LLMVariable"
  @spec as_llm_variable(t()) :: Dagger.LLMVariable.t()
  def as_llm_variable(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asLLMVariable")

    %Dagger.LLMVariable{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ListTypeDef"
  @spec as_list_type_def(t()) :: Dagger.ListTypeDef.t()
  def as_list_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asListTypeDef")

    %Dagger.ListTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Module"
  @spec as_module(t()) :: Dagger.Module.t()
  def as_module(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asModule")

    %Dagger.Module{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ModuleConfigClient"
  @spec as_module_config_client(t()) :: Dagger.ModuleConfigClient.t()
  def as_module_config_client(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asModuleConfigClient")

    %Dagger.ModuleConfigClient{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ModuleSource"
  @spec as_module_source(t()) :: Dagger.ModuleSource.t()
  def as_module_source(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asModuleSource")

    %Dagger.ModuleSource{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ObjectTypeDef"
  @spec as_object_type_def(t()) :: Dagger.ObjectTypeDef.t()
  def as_object_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asObjectTypeDef")

    %Dagger.ObjectTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type SDKConfig"
  @spec as_sdk_config(t()) :: Dagger.SDKConfig.t() | nil
  def as_sdk_config(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asSDKConfig")

    %Dagger.SDKConfig{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type ScalarTypeDef"
  @spec as_scalar_type_def(t()) :: Dagger.ScalarTypeDef.t()
  def as_scalar_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asScalarTypeDef")

    %Dagger.ScalarTypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Secret"
  @spec as_secret(t()) :: Dagger.Secret.t()
  def as_secret(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asSecret")

    %Dagger.Secret{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Service"
  @spec as_service(t()) :: Dagger.Service.t()
  def as_service(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asService")

    %Dagger.Service{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Socket"
  @spec as_socket(t()) :: Dagger.Socket.t()
  def as_socket(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asSocket")

    %Dagger.Socket{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type SourceMap"
  @spec as_source_map(t()) :: Dagger.SourceMap.t()
  def as_source_map(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asSourceMap")

    %Dagger.SourceMap{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type Terminal"
  @spec as_terminal(t()) :: Dagger.Terminal.t()
  def as_terminal(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asTerminal")

    %Dagger.Terminal{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "Retrieve the binding value, as type TypeDef"
  @spec as_type_def(t()) :: Dagger.TypeDef.t()
  def as_type_def(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("asTypeDef")

    %Dagger.TypeDef{
      query_builder: query_builder,
      client: binding.client
    }
  end

  @doc "The digest of the binding value"
  @spec digest(t()) :: {:ok, String.t()} | {:error, term()}
  def digest(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("digest")

    Client.execute(binding.client, query_builder)
  end

  @doc "A unique identifier for this Binding."
  @spec id(t()) :: {:ok, Dagger.BindingID.t()} | {:error, term()}
  def id(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("id")

    Client.execute(binding.client, query_builder)
  end

  @doc "The binding name"
  @spec name(t()) :: {:ok, String.t()} | {:error, term()}
  def name(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("name")

    Client.execute(binding.client, query_builder)
  end

  @doc "The binding type name"
  @spec type_name(t()) :: {:ok, String.t()} | {:error, term()}
  def type_name(%__MODULE__{} = binding) do
    query_builder =
      binding.query_builder |> QB.select("typeName")

    Client.execute(binding.client, query_builder)
  end
end

defimpl Jason.Encoder, for: Dagger.Binding do
  def encode(binding, opts) do
    {:ok, id} = Dagger.Binding.id(binding)
    Jason.Encode.string(id, opts)
  end
end

defimpl Nestru.Decoder, for: Dagger.Binding do
  def decode_fields_hint(_struct, _context, id) do
    {:ok, Dagger.Client.load_binding_from_id(Dagger.Global.dag(), id)}
  end
end
