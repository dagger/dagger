defmodule Dagger.Codegen.Elixir.Module do
  @moduledoc false

  # TODO: retire this and find a better way
  @id_modules_map %{
    "CacheVolumeID" => "CacheVolume",
    "ContainerID" => "Container",
    "DirectoryID" => "Directory",
    "EnvVariableID" => "EnvVariable",
    "FieldTypeDefID" => "FieldTypeDef",
    "FileID" => "File",
    "FunctionArgID" => "FunctionArg",
    "FunctionCallArgValueID" => "FunctionCallArgValue",
    "FunctionCallID" => "FunctionCall",
    "FunctionID" => "Function",
    "GeneratedCodeID" => "GeneratedCode",
    "GitRefID" => "GitRef",
    "GitRepositoryID" => "GitRepository",
    "HostID" => "Host",
    "InterfaceTypeDefID" => "InterfaceTypeDef",
    "LabelID" => "Label",
    "ListTypeDefID" => "ListTypeDef",
    "ModuleConfigID" => "ModuleConfig",
    "ModuleID" => "Module",
    "ObjectTypeDefID" => "ObjectTypeDef",
    "PortID" => "Port",
    "SecretID" => "Secret",
    "ServiceID" => "Service",
    "SocketID" => "Socket",
    "TypeDefID" => "TypeDef"
  }

  defmacro id_modules(), do: quote(do: Map.keys(@id_modules_map))

  def id_module_to_module(id_mod), do: Map.fetch!(@id_modules_map, id_mod)

  def format_name(name) when is_binary(name) do
    name
    |> Macro.camelize()
    |> String.to_atom()
  end

  def from_name("Query") do
    from_name("Client")
  end

  def from_name(name) do
    Module.concat([Dagger, format_name(name)])
  end
end
