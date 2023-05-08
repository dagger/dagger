defmodule Dagger do
  @moduledoc """
  The [Dagger](https://dagger.io/) SDK for Elixir.

  See `getting_start.livemd` for starter point.
  """

  use Dagger.QueryBuilder

  defstruct [:client, :query]

  @doc """
  Connecting to Dagger.

  ## Options

  #{NimbleOptions.docs(Dagger.Client.connect_schema())}
  """
  def connect(opts \\ []) do
    with {:ok, client} <- Dagger.Client.connect(opts) do
      {:ok,
       %Dagger.Query{
         client: client,
         selection: query()
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
  def disconnect(%Dagger.Query{client: client}) do
    Dagger.Client.disconnect(client)
  end
end
