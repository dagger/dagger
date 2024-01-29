# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Socket do
  @moduledoc "A Unix or TCP/IP socket that can be mounted into a container."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "A unique identifier for this Socket."
    @spec id(t()) :: {:ok, Dagger.SocketID.t()} | {:error, term()}
    def id(%__MODULE__{} = socket) do
      selection = select(socket.selection, "id")
      execute(selection, socket.client)
    end
  )
end
