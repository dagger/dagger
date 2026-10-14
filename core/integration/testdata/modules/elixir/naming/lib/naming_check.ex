defmodule NamingCheck do
  @moduledoc false

  use Dagger.Mod.Object, name: "NamingCheck"

  defn exported() :: String.t() do
    Code.ensure_loaded!(Dagger.GitBundle)

    "new=#{function_exported?(Dagger.GitBundle, :prerequisite_shas, 1)} " <>
      "legacy=#{function_exported?(Dagger.GitBundle, :prerequisite_sh_as, 1)}"
  end

  defn http_url_for(host_name: String.t()) :: String.t() do
    "https://" <> host_name
  end
end
