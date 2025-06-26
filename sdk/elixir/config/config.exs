# WIP
# TBD: Not sure if its the correct way to configure otel, we could do it programmatically if this is not suitable
import Config

nearly_immediate_ms = 100

normalize_protocol = fn protocol ->
  case protocol do
    "http/protobuf" -> :http_protobuf
    "grpc" -> :grpc
  end
end

# use environment from dagger or jaeger for testing purpose
otlp_endpoint = System.get_env("OTEL_EXPORTER_OTLP_ENDPOINT") || "http://localhost:4318"
otlp_protocol = System.get_env("OTEL_EXPORTER_OTLP_PROTOCOL") || "http/protobuf"

otlp_traces_endpoint =
  System.get_env("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") || otlp_endpoint

otlp_traces_protocol = System.get_env("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL") || "http/protobuf"

config :opentelemetry,
  resource: %{
    service: %{name: "dagger.io/sdk.elixir"},
    schema_url: "https://opentelemetry.io/schemas/1.21.0"
  },
  processors: [
    {Dagger.Telemetry.LiveProcessor,
     %{
       bsp_scheduled_delay_ms: nearly_immediate_ms,
       exporter: {
         :opentelemetry_exporter,
         %{
            protocol: otlp_protocol |> normalize_protocol.(),
            endpoints: [otlp_endpoint <> "/v1/traces"]
         }
       }
     }}
  ]

config :opentelemetry_exporter,
  otlp_traces_endpoint: otlp_traces_endpoint <> "/v1/traces",
  otlp_traces_protocol: otlp_traces_protocol |> normalize_protocol.()

# otlp_traces_live: System.get_env("OTEL_EXPORTER_OTLP_TRACES_LIVE")
