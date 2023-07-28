# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Port do
  @moduledoc "A port exposed by a container."
  @type t() :: %__MODULE__{
          description: String.t() | nil,
          port: integer(),
          protocol: Dagger.NetworkProtocol.t()
        }
  defstruct [:description, :port, :protocol]
end
