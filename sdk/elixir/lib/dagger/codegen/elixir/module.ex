defmodule Dagger.Codegen.Elixir.Module do
  @moduledoc false

  @id_modules_map %{
    "CacheVolumeID" => "CacheVolume",
    "ContainerID" => "Container",
    "DirectoryID" => "Directory",
    "FileID" => "File",
    "ProjectCommandID" => "ProjectCommand",
    "ProjectID" => "Project",
    "SecretID" => "Secret",
    "SocketID" => "Socket",
    "ServiceID" => "Service"
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
