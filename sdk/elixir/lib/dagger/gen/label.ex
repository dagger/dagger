# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Label do
  @moduledoc "A simple key value object that represents a label."
  @type t() :: %__MODULE__{name: String.t(), value: String.t()}
  @derive Nestru.Decoder
  defstruct [:name, :value]
end
