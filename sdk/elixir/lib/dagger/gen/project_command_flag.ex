# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.ProjectCommandFlag do
  @moduledoc "A flag accepted by a project command."
  @type t() :: %__MODULE__{description: String.t() | nil, name: String.t()}
  @derive Nestru.Decoder
  defstruct [:description, :name]
end
