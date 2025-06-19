defmodule Dagger.Telemetry.Propagator do
  @moduledoc """
  A simple Wrapper around :otel_propagator_trace_context
  """

  defp carrier_get(header_key, carrier), do: Map.get(carrier, header_key)
  defp carrier_set(header_key, value, carrier), do: Map.put(carrier, header_key, value)

  @spec inject(:otel_ctx.t()) :: :otel_propagator.carrier()
  def inject(ctx \\ nil) do
    ctx =
      case ctx do
        nil -> :otel_ctx.get_current()
        _ -> ctx
      end

    :otel_propagator_trace_context.inject(ctx, %{}, &carrier_set/3, nil)
  end

  @spec extract(map()) :: :otel_ctx.t()
  def extract(traceparent) do
    :otel_propagator_trace_context.extract(
      %{},
      %{"traceparent" => traceparent, "tracestate" => []},
      nil,
      &carrier_get/2,
      nil
    )
  end
end
