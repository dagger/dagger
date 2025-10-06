defmodule SelfObject do
  @moduledoc false

  use Dagger.Mod.Object, name: "SelfObject"

  object do
    field :message, String.t()
  end

  defn foo(self) :: SelfObject.t() do
    %{self | message: "bar"}
  end
end
