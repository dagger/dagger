defmodule ObjectMod do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectMod"

  defn accept_string(name: String.t()) :: String.t() do
    "Hello, #{name}"
  end

  defn accept_string2(name: binary()) :: binary() do
    "Hello, #{name}"
  end

  defn accept_integer(name: integer()) :: integer() do
    "Hello, #{name}"
  end

  defn accept_boolean(name: boolean()) :: String.t() do
    "Hello, #{name}"
  end

  defn empty_args() :: String.t() do
    "Empty args"
  end

  defn accept_and_return_module(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
  end

  defn accept_list(alist: list(String.t())) :: String.t() do
    Enum.join(alist, ",")
  end

  defn accept_list2(alist: [String.t()]) :: String.t() do
    Enum.join(alist, ",")
  end

  defn optional_arg(s: String.t() | nil) :: String.t() do
    "Hello, #{s}"
  end

  defn type_option(
         dir:
           {Dagger.Directory.t() | nil,
            doc: "The directory to run on.",
            default_path: "/sdk/elixir",
            ignore: ["deps", "_build"]}
       ) :: String.t() do
    Dagger.Directory.id(dir)
  end

  defn return_void() :: Dagger.Void.t() do
    :ok
  end
end
