# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Port do
  @moduledoc "A port exposed by a container."

  use Dagger.Core.QueryBuilder

  @derive Dagger.ID

  defstruct [:selection, :client]

  @type t() :: %__MODULE__{}

  @doc "The port description."
  @spec description(t()) :: {:ok, String.t() | nil} | {:error, term()}
  def description(%__MODULE__{} = port) do
    selection =
      port.selection |> select("description")

    execute(selection, port.client)
  end

  @doc "Skip the health check when run as a service."
  @spec experimental_skip_healthcheck(t()) :: {:ok, boolean()} | {:error, term()}
  def experimental_skip_healthcheck(%__MODULE__{} = port) do
    selection =
      port.selection |> select("experimentalSkipHealthcheck")

    execute(selection, port.client)
  end

  @doc "A unique identifier for this Port."
  @spec id(t()) :: {:ok, Dagger.PortID.t()} | {:error, term()}
  def id(%__MODULE__{} = port) do
    selection =
      port.selection |> select("id")

    execute(selection, port.client)
  end

  @doc "The port number."
  @spec port(t()) :: {:ok, integer()} | {:error, term()}
  def port(%__MODULE__{} = port) do
    selection =
      port.selection |> select("port")

    execute(selection, port.client)
  end

  @doc "The transport layer protocol."
  @spec protocol(t()) :: Dagger.NetworkProtocol.t()
  def protocol(%__MODULE__{} = port) do
    selection =
      port.selection |> select("protocol")

    execute(selection, port.client)
  end
end
