defmodule ObjectMod do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectMod"

  defn accept_string(name: String.t()) :: String.t() do
    "Hello, #{name}"
  end

  defn accept_string2(name: binary()) :: binary() do
    "Hello, #{name}"
  end

  defn accept_integer(value: integer()) :: integer() do
    value
  end

  defn accept_float(value: float()) :: float() do
    value
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

  object do
  end

  defn only_self_arg(_self) :: Dagger.Void.t() do
    :ok
  end

  defn mix_self_and_args(_self, name: String.t()) :: Dagger.Void.t() do
    name
  end
end

defmodule PrimitiveTypeArgs do
  use Dagger.Mod.Object, name: "PrimitiveTypeArgs"

  defn accept_string(name: String.t()) :: String.t() do
    "Hello, #{name}"
  end

  defn accept_string2(name: binary()) :: binary() do
    "Hello, #{name}"
  end

  defn accept_integer(value: integer()) :: integer() do
    value
  end

  defn accept_float(value: float()) :: float() do
    value
  end

  defn accept_boolean(name: boolean()) :: String.t() do
    "Hello, #{name}"
  end
end

defmodule EmptyArgs do
  use Dagger.Mod.Object, name: "EmptyArgs"

  defn empty_args() :: String.t() do
    "Empty args"
  end
end

defmodule ObjectArgAndReturn do
  use Dagger.Mod.Object, name: "ObjectArgAndReturn"

  defn accept_and_return_module(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
  end
end

defmodule ListArgs do
  use Dagger.Mod.Object, name: "ListArg"

  defn accept_list(alist: list(String.t())) :: String.t() do
    Enum.join(alist, ",")
  end

  defn accept_list2(alist: [String.t()]) :: String.t() do
    Enum.join(alist, ",")
  end
end

defmodule OptionalArgs do
  use Dagger.Mod.Object, name: "OptionalArgs"

  defn optional_arg(s: String.t() | nil) :: String.t() do
    "Hello, #{s}"
  end
end

defmodule ArgOptions do
  use Dagger.Mod.Object, name: "ArgOptions"

  defn type_option(
         dir:
           {Dagger.Directory.t() | nil,
            doc: "The directory to run on.",
            default_path: "/sdk/elixir",
            ignore: ["deps", "_build"]}
       ) :: String.t() do
    Dagger.Directory.id(dir)
  end
end

defmodule ReturnVoid do
  use Dagger.Mod.Object, name: "ReturnVoid"

  defn return_void() :: Dagger.Void.t() do
    :ok
  end
end

defmodule SelfObject do
  use Dagger.Mod.Object, name: "SelfObject"

  object do
  end

  defn only_self_arg(_self) :: Dagger.Void.t() do
    :ok
  end

  defn mix_self_and_args(_self, name: String.t()) :: Dagger.Void.t() do
    name
  end
end

defmodule ConstructorFunction do
  use Dagger.Mod.Object, name: "ConstructorFunction"

  object do
    field(:name, String.t())
  end

  defn init(name: String.t()) :: ConstructorFunction.t() do
    %__MODULE__{name: name}
  end
end
