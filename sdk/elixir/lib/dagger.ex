defmodule Dagger do
  @moduledoc """
  The [Dagger](https://dagger.io/) SDK for Elixir.

  See `getting_start.livemd` for starter point.
  """

  @doc """
  Connecting to Dagger.

  ## Options

  #{NimbleOptions.docs(Dagger.Core.Client.connect_schema())}
  """
  def connect(opts \\ []) do
    with {:ok, graphql_client} <- Dagger.Core.Client.connect(opts) do
      client = %Dagger.Client{
        client: graphql_client,
        selection: Dagger.Core.QueryBuilder.Selection.query()
      }

      case Dagger.Client.check_version_compatibility(
             client,
             Dagger.Core.EngineConn.engine_version()
           ) do
        {:error, reason} -> IO.warn("failed to check version compatibility: #{inspect(reason)}")
        _ -> nil
      end

      {:ok, client}
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
  def close(%Dagger.Client{client: client}) do
    Dagger.Core.Client.close(client)
  end
end
