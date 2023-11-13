# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Secret do
  @moduledoc "A reference to a secret value, which can be handled more safely than the value itself."
  use Dagger.Core.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "The identifier for this secret."
    @spec id(t()) :: {:ok, Dagger.SecretID.t()} | {:error, term()}
    def id(%__MODULE__{} = secret) do
      selection = select(secret.selection, "id")
      execute(selection, secret.client)
    end
  )

  (
    @doc "The value of this secret."
    @spec plaintext(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def plaintext(%__MODULE__{} = secret) do
      selection = select(secret.selection, "plaintext")
      execute(selection, secret.client)
    end
  )
end
