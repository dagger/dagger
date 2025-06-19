defmodule Dagger.Telemetry.LiveProcessor do
  @moduledoc """
  Live span Processor implementation It's a SpanProcessor whose on_start calls on_end on the underlying
  SpanProcessor in order to send live telemetry.
  """

  defstruct [:processor]

  def start_link(conf) do
    conf |> IO.inspect(label: "conf::")
    :opentelemetry.set_text_map_extractor(:otel_propagator_trace_context)
    :opentelemetry.set_text_map_injector(:otel_propagator_trace_context)

    :otel_batch_processor.start_link(conf)
  end

  @behaviour :otel_span_processor

  @impl :otel_span_processor
  def on_start(ctx, span, config) do
    :otel_batch_processor.on_start(ctx, span, config)
    on_end(span, [])
    span
  end

  @impl :otel_span_processor
  def on_end(span, config \\ []) do
    :otel_batch_processor.on_end(span, config)
    span
  end

  @impl :otel_span_processor
  def force_flush(config) do
    :otel_batch_processor.force_flush(config)
  end
end
