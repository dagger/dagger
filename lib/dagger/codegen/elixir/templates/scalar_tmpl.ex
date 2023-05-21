defmodule Dagger.Codegen.Elixir.Templates.Scalar do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function
  alias Dagger.Codegen.Elixir.Module, as: Mod

  @required_mods %{
    "ContainerID" => "Container",
    "CacheID" => "CacheVolume",
    "DirectoryID" => "Directory",
    "FileID" => "File",
    "SecretID" => "Secret",
    "SocketID" => "Socket"
  }

  @support_gen_fun Map.keys(@required_mods)

  def render(%{"name" => name, "description" => desc, "private" => %{mod_name: mod_name}})
      when name in @support_gen_fun do
    required_name = @required_mods[name]

    required_mod = Module.concat([Dagger, Mod.format_name(required_name)])

    required_var =
      required_name
      |> Function.format_var_name()
      |> Macro.var(__MODULE__)

    doc = "Get ID from `#{Function.format_var_name(required_name)}`."

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: String.t()

        @doc unquote(doc)
        def get_id(%unquote(required_mod){} = unquote(required_var)) do
          unquote(required_var)
          |> unquote(required_mod).id()
        end
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
