# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.EnvVariable do
  @moduledoc "A simple key value object that represents an environment variable."
  @type t() :: %__MODULE__{name: String.t(), value: String.t()}
  defstruct [:name, :value]
end
