defmodule Dagger.Client do
  @moduledoc """
  The Dagger client.
  """

  alias Dagger.EngineConn

  defstruct [:req, :conn, :opts]

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

  def connect_schema() do
    [
      workdir: [
        type: :string,
        doc: "Sets the engine workdir."
      ],
      config_path: [
        type: :string,
        doc: "Sets the engine config path."
      ],
      log_output: [
        type: :atom,
        doc: "Sets the progress writer."
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

  def disconnect(%__MODULE__{conn: conn}) do
    with :quit <- EngineConn.disconnect(conn) do
      :ok
    end
  end

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
