import logging
import os
from typing import Final

from opentelemetry import context, propagate, trace
from opentelemetry.environment_variables import (
    OTEL_PYTHON_TRACER_PROVIDER,
    OTEL_TRACES_EXPORTER,
)
from opentelemetry.sdk import trace as sdktrace
from opentelemetry.sdk.environment_variables import (
    OTEL_EXPORTER_OTLP_ENDPOINT,
    OTEL_EXPORTER_OTLP_INSECURE,
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT,
    OTEL_EXPORTER_OTLP_TRACES_INSECURE,
    OTEL_SDK_DISABLED,
    OTEL_SERVICE_NAME,
)
from opentelemetry.sdk.trace.export import BatchSpanProcessor, SpanExporter
from opentelemetry.trace import get_tracer_provider, propagation

__all__ = [
    "get_tracer",
    "initialize",
    "otel_configured",
    "otel_enabled",
    "shutdown",
]

SERVICE_NAME: Final = "dagger-python-sdk"

# The variables of opentelemetry-instrumentation-logging, named here because
# importing that package costs wrapt and packaging on every module call.
OTEL_PYTHON_LOG_CORRELATION: Final = "OTEL_PYTHON_LOG_CORRELATION"
OTEL_PYTHON_LOG_FORMAT: Final = "OTEL_PYTHON_LOG_FORMAT"
OTEL_PYTHON_LOG_LEVEL: Final = "OTEL_PYTHON_LOG_LEVEL"

logger = logging.getLogger(__name__)


def initialize(*, debug: bool = False):
    """Configure telemetry.

    If debug is True, enables console exporters.
    """
    _DaggerPropagationConfigurator().configure()
    _DaggerOtelConfigurator().configure(debug=debug)


def get_tracer() -> trace.Tracer:
    """Returns a tracer to use with Dagger."""
    initialize()
    return trace.get_tracer("dagger.io/sdk.python")


def shutdown():
    """Process all spans that have not yet been processed."""
    # TODO: set a timeout

    tracer_provider = get_tracer_provider()
    # Provider shutdown is called automatically on exit, we just need the forced
    # flush but might as well shutdown now too.

    if isinstance(tracer_provider, sdktrace.TracerProvider):
        tracer_provider.force_flush()
        tracer_provider.shutdown()


def otel_configured() -> bool:
    """Checks for OpenTelemetry configuration via OTEL_ environment variables."""
    return any(k for k in os.environ if k.startswith("OTEL_"))


def otel_enabled() -> bool:
    """Checks whether OpenTelemetry instrumentation is not disabled."""
    return os.getenv(OTEL_SDK_DISABLED, "").strip().lower() != "true"


def live_traces_enabled() -> bool:
    return os.getenv("OTEL_EXPORTER_OTLP_TRACES_LIVE") is not None


class _BaseConfigurator:
    """Base configurator singleton, that ensures configuration only happens once.

    Not opentelemetry.sdk._configuration's own base: importing that module
    loads the logs and metrics SDKs, about 20 ms per module call.
    """

    _instance = None
    _is_configured: bool = False

    def __new__(cls, *args, **kwargs):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def _configure(self, **kwargs):
        raise NotImplementedError

    def configure(self, **kwargs):
        if self._is_configured:
            return

        self._configure(**kwargs)
        self._is_configured = True


class _DaggerPropagationConfigurator(_BaseConfigurator):
    # NB: This configuration should be applied before any other telemetry
    # code runs, to ensure the context has the right traceparent.
    def _configure(self, **kwargs):
        if parent := os.getenv("TRACEPARENT"):
            if propagation.get_current_span().get_span_context().is_valid:
                return

            logger.debug("Found TRACEPARENT", extra={"value": parent})
            ctx = propagate.extract({"traceparent": parent})
            context.attach(ctx)


class LiveSpanProcessor(sdktrace.SynchronousMultiSpanProcessor):
    """Live span processor implementation.

    It's a SpanProcessor whose on_start calls on_end on the underlying
    SpanProcessor in order to send live telemetry.
    """

    def __init__(self, exp: SpanExporter):
        super().__init__()
        self.add_span_processor(BatchSpanProcessor(exp, schedule_delay_millis=100))

    def on_start(self, span: sdktrace.Span, parent_context=None) -> None:
        return self.on_end(span)


class _LazyExporter(SpanExporter):
    """Imports the configured exporter on its first export.

    A module call usually ends without a span, and the OTLP exporter
    imports requests and protobuf, about 40 ms per call.
    """

    def __init__(self, name: str):
        self._name = name
        self._exporter: SpanExporter | None = None

    def _get(self) -> SpanExporter:
        if self._exporter is None:
            from opentelemetry.sdk._configuration import (
                _get_exporter_entry_point,
                _import_exporters,
            )

            name = _get_exporter_entry_point(self._name, "traces")
            classes, _, _ = _import_exporters([name], [], [])
            self._exporter = classes[name]()
        return self._exporter

    def export(self, spans):
        return self._get().export(spans)

    def shutdown(self):
        if self._exporter is not None:
            self._exporter.shutdown()

    def force_flush(self, timeout_millis: int = 30000) -> bool:
        return self._exporter is None or self._exporter.force_flush(timeout_millis)


def _init_tracing(exporters: dict[str, type[SpanExporter]]):
    # By default this is a NoOpTracerProvider, unless OTEL_PYTHON_TRACER_PROVIDER
    # is set, which is done in _prepare_env.
    provider = get_tracer_provider()

    if isinstance(provider, sdktrace.TracerProvider):
        for exporter_class in exporters.values():
            proc_cls = (
                LiveSpanProcessor if live_traces_enabled() else BatchSpanProcessor
            )
            provider.add_span_processor(proc_cls(exporter_class()))


def _correlate_logs():
    logging.basicConfig(
        format=os.environ[OTEL_PYTHON_LOG_FORMAT],
        level=logging.getLevelNamesMapping().get(
            os.environ[OTEL_PYTHON_LOG_LEVEL].upper(), logging.INFO
        ),
    )
    old_factory = logging.getLogRecordFactory()

    def factory(*args, **kwargs):
        record = old_factory(*args, **kwargs)
        ctx = trace.get_current_span().get_span_context()
        record.otelSpanID = format(ctx.span_id, "016x") if ctx.is_valid else "0"
        record.otelTraceID = format(ctx.trace_id, "032x") if ctx.is_valid else "0"
        record.otelTraceSampled = ctx.trace_flags.sampled if ctx.is_valid else False
        record.otelServiceName = SERVICE_NAME
        return record

    logging.setLogRecordFactory(factory)


class _DaggerOtelConfigurator(_BaseConfigurator):
    exporters = ("otlp",)

    # NB: This is based on opentelemetry.sdk._configuration._OtelSDKConfigurator
    # which is experimental. Instead of importing just the configurator, we're
    # importing several private functions because we need more control over
    # the initialization of tracing exporters but still want to reuse as
    # much of the existing logic as possible. Need to keep an eye on upstream
    # changes though.
    def _configure(self, **kwargs):
        if not otel_configured():
            logger.debug("Telemetry not configured")
            return

        if not otel_enabled():
            logger.debug("Telemetry disabled")
            return

        if kwargs.get("debug"):
            self.exporters += ("console",)

        logger.debug("Initializing telemetry")
        self._prepare_env()
        self._initialize()
        logger.debug("Telemetry initialized")

    def _prepare_env(self):
        """Prepare environment variables for auto-configuring the SDK."""
        # When a Resource is created, it defaults to the following env var
        # for the service name.
        os.environ.setdefault(OTEL_SERVICE_NAME, SERVICE_NAME)

        # The default is a NoOpProvider.
        os.environ.setdefault(OTEL_PYTHON_TRACER_PROVIDER, "sdk_tracer_provider")

        # Logging instrumentation.
        os.environ.setdefault(OTEL_PYTHON_LOG_CORRELATION, "true")
        os.environ.setdefault(OTEL_PYTHON_LOG_LEVEL, "warning")
        os.environ.setdefault(
            OTEL_PYTHON_LOG_FORMAT,
            "%(levelname)s [%(name)s]: %(message)s",
        )

        _vars = {
            OTEL_EXPORTER_OTLP_ENDPOINT: OTEL_EXPORTER_OTLP_INSECURE,
            OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: OTEL_EXPORTER_OTLP_TRACES_INSECURE,
        }
        for endpoint, insecure in _vars.items():
            if os.getenv(endpoint, "").startswith("http://"):
                os.environ.setdefault(insecure, "true")

    def _initialize(self):
        # NB: Dagger's engine only accepts Gauge metrics today (engine-side
        # exec resource monitoring). Emitting counters/histograms from modules
        # produces 500s and retry noise, so skip metric initialization by
        # passing an empty list for the metrics slot of _import_exporters.
        #
        # Also skip OTel log exporting. Dagger already captures module
        # stdout/stderr and forwards it as logs; installing OTel's logging
        # handler would duplicate Python logging records in the TUI.
        names = os.environ.get(OTEL_TRACES_EXPORTER, "")
        if names.strip().lower() == "none":
            names = ""
        trace_exporters = {
            name: (lambda name=name: _LazyExporter(name))
            for name in (n.strip() for n in names.split(",") if n.strip())
        }
        logger.debug(
            "Initializing traces telemetry with exporters: %s",
            ", ".join(trace_exporters) if trace_exporters else "none",
        )
        _init_tracing(trace_exporters)

        # The trace context goes into log records, as the logging instrumentor
        # puts it, without exporting them separately.
        _correlate_logs()
