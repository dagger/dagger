defmodule Dagger.Core.GraphQLClient do
  @moduledoc false

  alias Dagger.Core.GraphQL.Response

  @callback request(
              url :: String.t(),
              request_body :: iodata(),
              headers :: [tuple()],
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

    headers =
      []
      |> with_basic_auth(session_token)
      |> with_traceparent()

    with {:ok, request} <- json.encode(request),
         {:ok, status, result} <- client.request(url, request, headers, timeout: timeout),
         {:ok, map} <- json.decode(result) do
      response = Response.from_map(map)

      case status do
        200 -> {:ok, response}
        _ -> {:error, response}
      end
    else
      otherwise ->
        otherwise
    end
  end

  defp with_basic_auth(headers, session_token) when is_binary(session_token) do
    token = [session_token, ?:] |> IO.iodata_to_binary() |> Base.encode64()
    [{"authorization", ["Basic ", token]} | headers]
  end

  defp with_traceparent(headers, traceparent \\ System.get_env("TRACEPARENT")) do
    case traceparent do
      nil ->
        headers

      traceparent ->
        [{"traceparent", traceparent} | headers]
    end
  end
end
