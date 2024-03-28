defmodule Dagger.Core.GraphQLClient.Httpc do
  @moduledoc """
  `:httpc` HTTP adapter for GraphQL client.
  """

  def request(url, session_token, request_body, http_opts) do
    token = [session_token, ":"] |> IO.iodata_to_binary() |> Base.encode64()

    headers = [
      {~c"authorization", ["Basic ", token]}
    ]

    content_type = ~c"application/json"
    request = {url, headers, content_type, request_body}
    options = []

    case :httpc.request(:post, request, http_opts, options) do
      {:ok, {{_, status_code, _}, _, response}} ->
        {:ok, status_code, response}

      otherwise ->
        otherwise
    end
  end
end
