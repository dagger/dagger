defmodule PrimitiveTypeArgs do
  @moduledoc false
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

defmodule PrimitiveTypeDefaultArgs do
  @moduledoc false
  use Dagger.Mod.Object, name: "PrimitiveTypeDefaultArgs"

  defn accept_default_string(name: {String.t(), default: "Foo"}) :: String.t() do
    "Hello #{name}"
  end

  defn accept_default_integer(value: {integer(), default: 42}) :: integer() do
    value
  end

  defn accept_default_float(value: {float(), default: 1.6180342}) :: float() do
    value
  end

  defn accept_default_boolean(value: {boolean(), default: false}) :: boolean() do
    value
  end
end

defmodule EmptyArgs do
  @moduledoc false
  use Dagger.Mod.Object, name: "EmptyArgs"

  defn empty_args() :: String.t() do
    "Empty args"
  end
end

defmodule ObjectArgAndReturn do
  @moduledoc false
  use Dagger.Mod.Object, name: "ObjectArgAndReturn"

  defn accept_and_return_module(container: Dagger.Container.t()) :: Dagger.Container.t() do
    container
  end
end

defmodule ListArgs do
  @moduledoc false
  use Dagger.Mod.Object, name: "ListArg"

  defn accept_list(alist: list(String.t())) :: String.t() do
    Enum.join(alist, ",")
  end

  defn accept_list2(alist: [String.t()]) :: String.t() do
    Enum.join(alist, ",")
  end
end

defmodule OptionalArgs do
  @moduledoc false
  use Dagger.Mod.Object, name: "OptionalArgs"

  defn optional_arg(s: String.t() | nil) :: String.t() do
    "Hello, #{s}"
  end
end

defmodule ArgOptions do
  @moduledoc false
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
  @moduledoc false
  use Dagger.Mod.Object, name: "ReturnVoid"

  defn return_void() :: Dagger.Void.t() do
    :ok
  end
end

defmodule SelfObject do
  @moduledoc false
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
  @moduledoc false
  use Dagger.Mod.Object, name: "ConstructorFunction"

  object do
    field(:name, String.t())
  end

  defn init(name: String.t()) :: ConstructorFunction.t() do
    %__MODULE__{name: name}
  end
end

defmodule AcceptAndReturnScalar do
  @moduledoc false

  use Dagger.Mod.Object, name: "AcceptAndReturnScalar"

  defn accept(value: Dagger.Platform.t()) :: Dagger.Platform.t() do
    value
  end
end

defmodule AcceptAndReturnEnum do
  @moduledoc false

  use Dagger.Mod.Object, name: "AcceptAndReturnEnum"

  defn accept(value: Dagger.NetworkProtocol.t()) :: Dagger.NetworkProtocol.t() do
    value
  end
end

defmodule Deps.C do
  @moduledoc false

  use Dagger.Mod.Object, name: "C"

  object do
  end

  defn hello() :: String.t() do
    "Hello"
  end
end

defmodule Deps.B do
  @moduledoc false

  use Dagger.Mod.Object, name: "B"

  object do
  end

  defn hello() :: String.t() do
    "Hello"
  end
end

defmodule Deps.A do
  @moduledoc false

  use Dagger.Mod.Object, name: "A"

  object do
  end

  defn do_b() :: Deps.B.t() do
    %Deps.B{}
  end

  defn do_c() :: Deps.C.t() do
    %Deps.B{}
  end
end

defmodule Deps do
  @moduledoc false

  use Dagger.Mod.Object, name: "Deps"

  object do
  end

  defn do_a() :: Deps.A.t() do
    %Deps.A{}
  end
end

defmodule CustomEnum do
  @moduledoc false

  use Dagger.Mod.Object, name: "CustomEnum"

  defn scan(severity: SimpleEnum.t()) :: SimpleEnum.t() do
    severity
  end

  defn enum_opt(opt: EnumWithOption.t()) :: Dagger.Void.t() do
    _ = opt
    :ok
  end
end
