defmodule Dagger.Core.Client do
  @moduledoc """
  GraphQL client for Dagger.
  """

  alias Dagger.Core.EngineConn
  alias Dagger.Core.GraphQLClient
  alias Dagger.Core.QueryBuilder.Selection

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

  @doc false
  def query(%__MODULE__{connect_opts: connect_opts} = client, query)
      when is_binary(query) do
    GraphQLClient.request(client.url, EngineConn.token(client.conn), query, %{},
      timeout: connect_opts[:query_timeout] || :timer.minutes(5)
    )
  end

  @doc false
  def execute(selection, client) do
    q = Selection.build(selection)

    case query(client, q) do
      {:ok, %{"data" => nil, "errors" => errors}} ->
        {:error, %Dagger.QueryError{errors: errors}}

      {:ok, %{"data" => data}} ->
        {:ok, select_data(data, Selection.path(selection) |> Enum.reverse())}

      otherwise ->
        otherwise
    end
  end

  defp select_data(data, [sub_selection | path]) do
    case sub_selection |> String.split() do
      [selection] ->
        get_in(data, Enum.reverse([selection | path]))

      selections ->
        case get_in(data, Enum.reverse(path)) do
          data when is_list(data) -> Enum.map(data, &Map.take(&1, selections))
          data when is_map(data) -> Map.take(data, selections)
        end
    end
  end
end
