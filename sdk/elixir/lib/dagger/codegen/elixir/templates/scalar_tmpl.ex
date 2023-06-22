defmodule Dagger.Codegen.Elixir.Templates.Scalar do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Module, as: Mod

  @required_mods %{
    "CacheID" => "CacheVolume",
    "ContainerID" => "Container",
    "DirectoryID" => "Directory",
    "FileID" => "File",
    "ProjectCommandID" => "ProjectCommand",
    "ProjectID" => "Project",
    "SecretID" => "Secret",
    "SocketID" => "Socket"
  }

  @support_gen_fun Map.keys(@required_mods)

  def render(%{"name" => name, "description" => desc, "private" => %{mod_name: mod_name}})
      when name in @support_gen_fun do
    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: String.t()
      end
    end
  end

  def render(%{"name" => name, "description" => desc}) do
    mod_name = Module.concat([Dagger, Mod.format_name(name)])

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)
        @type t() :: String.t()
      end
    end
  end
end
