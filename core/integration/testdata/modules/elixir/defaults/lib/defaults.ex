defmodule Defaults do
  @moduledoc false

  use Dagger.Mod.Object, name: "Defaults"

  defn echo_else(value: String.t() | nil) :: String.t() do
    if(value, do: value, else: "default value if null")
  end

  defn file_name(file: {Dagger.File.t(), default_path: "dagger.json"}) :: String.t() do
    Dagger.File.name(file)
  end

  defn file_names(dir: {Dagger.Directory.t(), default_path: "lib"}) :: String.t() do
    with {:ok, entries} <- Dagger.Directory.entries(dir) do
      Enum.join(entries, " ")
    end
  end

  defn files_no_ignore(dir: {Dagger.Directory.t(), default_path: "."}) :: String.t() do
    with {:ok, entries} <- Dagger.Directory.entries(dir) do
      Enum.join(entries, " ")
    end
  end

  defn files_ignore(dir: {Dagger.Directory.t(), default_path: ".", ignore: ["mix.exs"]}) :: String.t() do
    with {:ok, entries} <- Dagger.Directory.entries(dir) do
      Enum.join(entries, " ")
    end
  end

  defn files_neg_ignore(dir: {Dagger.Directory.t(), default_path: ".", ignore: ["**", "!**/*.ex"]}) :: String.t() do
    with {:ok, entries} <- Dagger.Directory.entries(dir) do
      Enum.join(entries, " ")
    end
  end
end
