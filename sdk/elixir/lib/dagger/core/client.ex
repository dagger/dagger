defmodule Dagger.Core.Client do
  @moduledoc """
  GraphQL client for Dagger.
  """

  alias Dagger.Core.EngineConn
  alias Dagger.Core.ExecError
  alias Dagger.Core.GraphQL.Response
  alias Dagger.Core.GraphQLClient
  alias Dagger.Core.QueryBuilder, as: QB

  defstruct [:url, :conn, :connect_opts]

  @doc false
  def connect(opts \\ []) do
    with {:ok, opts} <- NimbleOptions.validate(opts, connect_schema()),
         {:ok, conn} <- EngineConn.get(opts) do
      host = EngineConn.host(conn)

      {:ok,
       %__MODULE__{
         url: "http://#{host}/query",
         conn: conn,
         connect_opts: opts
       }}
    end
  end

  @doc false
  def connect_schema() do
    [
      workdir: [
        type: :string,
        doc: "Sets the engine workdir."
      ],
      log_output: [
        type: {:or, [:atom, :pid]},
        doc: "The log device to write the progress.",
        default: :stderr
      ],
      connect_timeout: [
        type: :timeout,
        doc: "Sets timeout when connect to the engine.",
        default: :timer.seconds(10)
      ],
      query_timeout: [
        type: :timeout,
        doc: "Sets timeout when executing a query.",
        default: :infinity
      ]
    ]
  end

  @doc false
  def close(%__MODULE__{conn: conn}) do
    with :quit <- EngineConn.disconnect(conn) do
      :ok
    end
  end

  @doc """
  Send the raw GraphQL query to the Dagger.

  Returns a raw response from the Dagger.
  """
  def query(%__MODULE__{connect_opts: connect_opts} = client, query)
      when is_binary(query) do
    GraphQLClient.request(client.url, EngineConn.token(client.conn), query, %{},
      timeout: connect_opts[:query_timeout] || :timer.minutes(5)
    )
  end

  @doc """
  Send the `query_builder` to the Dagger and get the result from leaf node of
  the query.
  """
  def execute(client, query_builder) do
    q = QB.build(query_builder)

    case query(client, q) do
      {:ok, %Response{errors: [error]}} ->
        error =
          case error.extensions["_type"] do
            "EXEC_ERROR" ->
              error.extensions
              |> ExecError.from_map()
              |> ExecError.with_original_error(error)

            _ ->
              error
          end

        {:error, error}

      {:ok, %Response{data: data}} ->
        {:ok, select(data, QB.path(query_builder))}

      otherwise ->
        otherwise
    end
  end

  defp select(data, []), do: data

  defp select(data, _selectors) when is_list(data) do
    data
  end

  defp select(data, [selector | selectors]) do
    select(Map.get(data, selector), selectors)
  end
end
