defmodule Dagger.Core.GraphQLClient.Httpc do
  @moduledoc """
  `:httpc` HTTP adapter for GraphQL client.
  """

  def request(url, request_body, headers, http_opts) do
    headers = Enum.map(headers, fn {k, v} -> {String.to_charlist(k), v} end)
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
