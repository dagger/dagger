defmodule Dagger.Core.GraphQLClient do
  @moduledoc false

  @callback request(
              url :: String.t(),
              session_token :: String.t(),
              request_body :: iodata(),
              opts :: keyword()
            ) :: {:ok, status :: non_neg_integer(), response :: map()} | {:error, term()}

  @doc """
  Perform a request to GraphQL server.
  """
  def request(url, session_token, query, variables, opts \\ []) do
    client = Keyword.get(opts, :client, Dagger.Core.GraphQLClient.Httpc)
    # TODO: change to json erlang standard library when support OTP 27 exclusively.
    json = Keyword.get(opts, :json_library, Jason)
    timeout = Keyword.get(opts, :timeout, :infinity)
    request = %{query: query, variables: variables}

    with {:ok, request} <- json.encode(request),
         {:ok, 200, result} <- client.request(url, session_token, request, timeout: timeout) do
      json.decode(result)
    else
      {:ok, _, error} ->
        with {:ok, result} <- json.decode(error) do
          {:error, result}
        end

      otherwise ->
        otherwise
    end
  end
end
