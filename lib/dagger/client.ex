defmodule Dagger.Client do
  @moduledoc """
  The Dagger client.
  """

  alias Dagger.EngineConn

  defstruct [:req, :conn, :opts]

  def connect(opts \\ []) do
    with {:ok, conn} <- EngineConn.get() do
      host = EngineConn.host(conn)

      {:ok,
       %__MODULE__{
         req: Req.new(base_url: "http://#{host}") |> AbsintheClient.attach(),
         conn: conn,
         opts: opts
       }}
    end
  end

  def disconnect(%__MODULE__{conn: conn}) do
    EngineConn.disconnect(conn)
  end

  def query(%__MODULE__{opts: opts} = client, query) when is_binary(query) do
    Req.post(client.req,
      url: "/query",
      graphql: query,
      auth: {token(client), ""},
      receive_timeout: opts[:timeout] || 300_000
    )
  end

  defp token(%__MODULE__{conn: conn}) do
    EngineConn.token(conn)
  end
end
