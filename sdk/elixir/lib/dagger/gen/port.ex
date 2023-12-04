# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Port do
  @moduledoc "A port exposed by a container."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc ""
    @spec description(t()) :: {:ok, Dagger.String.t() | nil} | {:error, term()}
    def description(%__MODULE__{} = port) do
      selection = select(port.selection, "description")
      execute(selection, port.client)
    end
  )

  (
    @doc "A unique identifier for this Port."
    @spec id(t()) :: {:ok, Dagger.PortID.t()} | {:error, term()}
    def id(%__MODULE__{} = port) do
      selection = select(port.selection, "id")
      execute(selection, port.client)
    end
  )

  (
    @doc ""
    @spec port(t()) :: {:ok, Dagger.Int.t()} | {:error, term()}
    def port(%__MODULE__{} = port) do
      selection = select(port.selection, "port")
      execute(selection, port.client)
    end
  )

  (
    @doc ""
    @spec protocol(t()) :: {:ok, Dagger.NetworkProtocol.t()} | {:error, term()}
    def protocol(%__MODULE__{} = port) do
      selection = select(port.selection, "protocol")
      execute(selection, port.client)
    end
  )

  (
    @doc ""
    @spec skip_health_check(t()) :: {:ok, Dagger.Boolean.t()} | {:error, term()}
    def skip_health_check(%__MODULE__{} = port) do
      selection = select(port.selection, "skipHealthCheck")
      execute(selection, port.client)
    end
  )
end
