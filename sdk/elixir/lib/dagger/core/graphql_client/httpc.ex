defmodule Dagger.Core.GraphQLClient.Httpc do
  @moduledoc """
  `:httpc` HTTP adapter for GraphQL client.
  """

  alias OpenTelemetry, as: OTel

  def request(url, session_token, request_body, http_opts) do
    token = [session_token, ":"] |> IO.iodata_to_binary() |> Base.encode64()

    headers = [
      {~c"authorization", ["Basic ", token]}
    ]

    content_type = ~c"application/json"
    request = {url, headers, content_type, request_body}
    options = []

    :otel_propagator_trace_context.inject(
      get_context(),
      headers,
      &:otel_propagator_text_map.default_carrier_keys/1,
      &:otel_propagator_text_map.default_carrier_set/3
    )

    case :httpc.request(:post, request, http_opts, options) do
      {:ok, {{_, status_code, _}, _, response}} ->
        {:ok, status_code, response}

      otherwise ->
        otherwise
    end
  end

  def get_context(traceparent \\ System.get_env("TRACEPARENT")) do
    ctx = OTel.Ctx.get_current()
    span = OTel.Tracer.current_span_ctx(ctx)

    if OTel.Span.is_valid(span) do
      ctx
    else
      if traceparent do
        :otel_propagator_trace_context.extract(
          ctx,
          [{"traceparent", traceparent}],
          &:otel_propagator_text_map.default_carrier_keys/1,
          &:otel_propagator_text_map.default_carrier_get/2,
          []
        )
      else
        ctx
      end
    end
  end
end
