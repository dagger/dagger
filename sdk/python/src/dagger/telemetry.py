import logging
import os
from collections.abc import Callable
from typing import Final, Literal

from opentelemetry import context, propagate, trace
from opentelemetry.environment_variables import (
    OTEL_LOGS_EXPORTER,
    OTEL_METRICS_EXPORTER,
    OTEL_PYTHON_TRACER_PROVIDER,
    OTEL_TRACES_EXPORTER,
)
from opentelemetry.sdk import trace as sdktrace
from opentelemetry.sdk._configuration import _BaseConfigurator as _BaseSDKConfigurator
from opentelemetry.sdk._configuration import (
    _get_exporter_names,
    _import_exporters,
    _init_logging,
    _init_metrics,
)
from opentelemetry.sdk.environment_variables import (
    OTEL_EXPORTER_OTLP_ENDPOINT,
    OTEL_EXPORTER_OTLP_INSECURE,
    OTEL_EXPORTER_OTLP_LOGS_ENDPOINT,
    OTEL_EXPORTER_OTLP_LOGS_INSECURE,
    OTEL_EXPORTER_OTLP_METRICS_ENDPOINT,
    OTEL_EXPORTER_OTLP_METRICS_INSECURE,
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT,
    OTEL_EXPORTER_OTLP_TRACES_INSECURE,
    OTEL_SDK_DISABLED,
    OTEL_SERVICE_NAME,
)
from opentelemetry.sdk.trace.export import BatchSpanProcessor, SpanExporter
from opentelemetry.semconv.trace import SpanAttributes
from opentelemetry.trace import get_tracer_provider, propagation

__all__ = [
    "get_tracer",
    "initialize",
    "otel_configured",
    "otel_enabled",
    "shutdown",
]

SERVICE_NAME: Final = "dagger-python-sdk"

logger = logging.getLogger(__name__)


def initialize():
    """Configure telemetry."""
    _DaggerPropagationConfigurator().configure()
    _DaggerOtelConfigurator().configure()


def get_tracer() -> trace.Tracer:
    """Returns a tracer to use with Dagger."""
    initialize()
    return trace.get_tracer(
        "dagger.io/sdk.python",
        schema_url=SpanAttributes.SCHEMA_URL,
    )


def shutdown():
    """Process all spans that have not yet been processed."""
    provider = get_tracer_provider()

    if isinstance(provider, sdktrace.TracerProvider):
        provider.force_flush()
        # shutdown is called automatically on exit, we just need the forced
        # flush, but might as well shutdown now too
        provider.shutdown()


def otel_configured() -> bool:
    """Checks for OpenTelemetry configuration via OTEL_ environment variables."""
    return any(k for k in os.environ if k.startswith("OTEL_"))


def otel_enabled() -> bool:
    """Checks whether OpenTelemetry instrumentation is not disabled."""
    return os.getenv(OTEL_SDK_DISABLED, "").strip().lower() != "true"


def live_traces_enabled() -> bool:
    return os.getenv("OTEL_EXPORTER_OTLP_TRACES_LIVE") is not None


class _BaseConfigurator(_BaseSDKConfigurator):
    """Base configurator singleton, that ensures configuration only happens once."""

    _is_configured: bool = False

    def configure(self, **kwargs):
        if self._is_configured:
            return

        super().configure(**kwargs)
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


class _DaggerOtelConfigurator(_BaseConfigurator):
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

        # TODO: The following env vars should be set by the shim rather than in the SDK.

        for exporter in (
            OTEL_TRACES_EXPORTER,
            OTEL_LOGS_EXPORTER,
            OTEL_METRICS_EXPORTER,
        ):
            os.environ.setdefault(exporter, "otlp")

        _vars = {
            OTEL_EXPORTER_OTLP_ENDPOINT: OTEL_EXPORTER_OTLP_INSECURE,
            OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: OTEL_EXPORTER_OTLP_METRICS_INSECURE,
            OTEL_EXPORTER_OTLP_LOGS_ENDPOINT: OTEL_EXPORTER_OTLP_LOGS_INSECURE,
            OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: OTEL_EXPORTER_OTLP_TRACES_INSECURE,
        }
        for endpoint, insecure in _vars.items():
            if os.getenv(endpoint, "").startswith("http://"):
                os.environ.setdefault(insecure, "true")

    def _initialize(self):
        # NB: Fixed order, based on _import_exporters arguments.
        initializers: dict[Literal["traces", "metrics", "logs"], Callable] = {
            "traces": _init_tracing,
            "metrics": _init_metrics,
            "logs": _init_logging,
        }
        all_exporters = _import_exporters(
            *(_get_exporter_names(t) for t in initializers)
        )

        for (kind, init), exporters in zip(
            initializers.items(), all_exporters, strict=True
        ):
            logger.debug(
                "Initializing %s telemetry with exporters: %s",
                kind,
                ", ".join(exporters) if exporters else "none",
            )

            init(exporters)
