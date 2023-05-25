defmodule Dagger do
  @moduledoc """
  The [Dagger](https://dagger.io/) SDK for Elixir.

  See `getting_start.livemd` for starter point.
  """

  defstruct [:client, :query]

  @doc """
  Connecting to Dagger.

  ## Options

  #{NimbleOptions.docs(Dagger.Internal.Client.connect_schema())}
  """
  def connect(opts \\ []) do
    with {:ok, client} <- Dagger.Internal.Client.connect(opts) do
      {:ok,
       %Dagger.Query{
         client: client,
         selection: Dagger.QueryBuilder.Selection.query()
       }}
    end
  end

  @doc """
  Similar to `connect/1` but raise exception when found an error.
  """
  def connect!(opts \\ []) do
    case connect(opts) do
      {:ok, query} -> query
      error -> raise "Cannot connect to Dagger engine, cause: #{inspect(error)}"
    end
  end

  @doc """
  Disconnecting Dagger.
  """
  def close(%Dagger.Query{client: client}) do
    Dagger.Internal.Client.close(client)
  end
end
