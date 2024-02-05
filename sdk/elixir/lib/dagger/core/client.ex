defmodule Dagger.Core.Client do
  @moduledoc """
  GraphQL client for Dagger.
  """

  alias Dagger.Core.EngineConn

  defstruct [:req, :conn, :opts]

  @doc false
  def connect(opts \\ []) do
    with {:ok, opts} <- NimbleOptions.validate(opts, connect_schema()),
         {:ok, conn} <- EngineConn.get(opts) do
      host = EngineConn.host(conn)

      {:ok,
       %__MODULE__{
         req: Req.new(base_url: "http://#{host}") |> AbsintheClient.attach(),
         conn: conn,
         opts: opts
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
  def query(%__MODULE__{opts: opts} = client, query) when is_binary(query) do
    Req.post(client.req,
      url: "/query",
      graphql: query,
      auth: {token(client), ""},
      receive_timeout: opts[:query_timeout] || 300_000
    )
  end

  defp token(%__MODULE__{conn: conn}) do
    EngineConn.token(conn)
  end
end
