defmodule Dagger.Telemetry do
  defmodule Tracing do
    defmacro __using__(_opts) do
      quote do
        require OpenTelemetry.Tracer, as: Tracer

        require unquote(__MODULE__)
        import unquote(__MODULE__)
      end
    end
  end
end
