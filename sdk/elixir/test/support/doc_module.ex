defmodule DocModule do
  @moduledoc """
  The module documentation.
  """
  use Dagger.Mod.Object, name: "DocModule"

  @doc """
  Echo the output.
  """
  defn echo(name: String.t()) :: String.t() do
    "Hello, #{name}"
  end

  defn no_fun_doc() :: String.t() do
    "Hello"
  end

  @doc false
  defn hidden_fun_doc() :: String.t() do
    "Hello"
  end
end

defmodule NoDocModule do
  @moduledoc false

  use Dagger.Mod.Object, name: "NoDocModule"

  defn echo() :: String.t() do
    "Hello"
  end
end

defmodule HiddenDocModule do
  @moduledoc false

  use Dagger.Mod.Object, name: "HiddenDocModule"

  defn echo() :: String.t() do
    "Hello"
  end
end
