if Code.ensure_loaded?(Req) do
  defmodule Dagger.Core.GraphQLClient.Req do
    @moduledoc """
    `:req` adapter for GraphQL client.
    """

    @behaviour Dagger.Core.GraphQLClient

    @impl true
    def request(url, request_body, headers, http_opts) do
      timeout = http_opts[:timeout] || :infinity

      with {:ok, resp} <-
             Req.post(
               url: url,
               body: request_body,
               headers: [{"content-type", "application/json"} | headers],
               decode_body: false,
               connect_options: [timeout: timeout],
               receive_timeout: timeout
             ) do
        {:ok, resp.status, resp.body}
      end
    end
  end
end
