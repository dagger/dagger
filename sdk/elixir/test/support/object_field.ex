defmodule ObjectField do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectField"

  object do
    field(:name, String.t())
  end
end

defmodule ObjectFieldOptional do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectFieldOptional"

  object do
    field(:name, String.t() | nil)
  end
end

defmodule ObjectFieldMixesOptionalAndRequired do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectFieldMixesOptionalAndRequired"

  object do
    field(:name, String.t() | nil)
    field(:key, String.t())
  end
end

defmodule ObjectFiedAndFunction do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectFiedAndFunction"

  object do
    field(:name, String.t() | nil)
  end

  defn with_name(name: String.t()) :: ObjectFieldAndFunction.t() do
    %ObjectFiedAndFunction{name: name}
  end

  defn fan_out(name: String.t()) :: [ObjectFieldAndFunction.t()] do
    [
      %ObjectFiedAndFunction{name: name <> "1"},
      %ObjectFiedAndFunction{name: name <> "2"}
    ]
  end
end

defmodule ObjectDecoder do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectDecoder"

  object do
    field(:value, String.t() | nil)
    field(:object_field, ObjectField.t())
  end
end

defmodule ObjectDecodeId do
  @moduledoc false

  use Dagger.Mod.Object, name: "ObjectDecoder"

  object do
    field(:container, Dagger.Container.t())
  end
end
