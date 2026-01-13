"""OpenTelemetry SDK configuration for pytest instrumentation."""

import logging
import os
import threading
from typing import Optional

from opentelemetry import context, propagate, trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.sdk import _logs as sdklogs
from opentelemetry.sdk import trace as sdktrace
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.trace.export import BatchSpanProcessor, SpanExporter

logger = logging.getLogger(__name__)


def _get_otlp_exporter() -> Optional[SpanExporter]:
    """Get OTLP span exporter if configured."""
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT") or os.environ.get(
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
    )
    if not endpoint:
        return None

    try:
        # Try gRPC first, fall back to HTTP
        if endpoint.startswith("http://") or endpoint.startswith("https://"):
            from opentelemetry.exporter.otlp.proto.http.trace_exporter import (
                OTLPSpanExporter,
            )

            return OTLPSpanExporter()
        else:
            from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import (
                OTLPSpanExporter,
            )

            return OTLPSpanExporter()
    except ImportError:
        logger.warning("OTLP exporter not available, spans will not be exported")
        return None


def _get_otlp_log_exporter():
    """Get OTLP log exporter if configured."""
    endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT") or os.environ.get(
        "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
    )
    if not endpoint:
        return None

    try:
        if endpoint.startswith("http://") or endpoint.startswith("https://"):
            from opentelemetry.exporter.otlp.proto.http._log_exporter import (
                OTLPLogExporter,
            )

            return OTLPLogExporter()
        else:
            from opentelemetry.exporter.otlp.proto.grpc._log_exporter import (
                OTLPLogExporter,
            )

            return OTLPLogExporter()
    except ImportError:
        logger.warning("OTLP log exporter not available, logs will not be exported")
        return None


class TelemetryConfig:
    """Singleton configuration for OpenTelemetry in pytest.

    Handles initialization of tracer and logger providers, and extraction
    of TRACEPARENT from environment for trace context propagation.

    Thread-safe: Uses a lock to protect initialization in concurrent scenarios.
    """

    _instance: Optional["TelemetryConfig"] = None
    _is_configured: bool = False
    _tracer_provider: Optional[sdktrace.TracerProvider] = None
    _logger_provider: Optional[sdklogs.LoggerProvider] = None
    _lock: threading.Lock = threading.Lock()

    def __new__(cls) -> "TelemetryConfig":
        with cls._lock:
            if cls._instance is None:
                cls._instance = super().__new__(cls)
            return cls._instance

    def configure(self) -> None:
        """Initialize OpenTelemetry SDK with parent context from TRACEPARENT."""
        with self._lock:
            if self._is_configured:
                return

            # Extract and attach parent context from TRACEPARENT env var
            self._attach_parent_context()

            # Set up environment defaults for insecure connections
            self._prepare_env()

            # Initialize tracer provider
            self._init_tracer()

            # Initialize logger provider
            self._init_logger()

            self._is_configured = True
            logger.debug("pytest-otel telemetry configured")

    def _prepare_env(self) -> None:
        """Prepare environment for OTLP configuration."""
        # Auto-configure insecure flag for http:// endpoints
        endpoint_vars = [
            ("OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_INSECURE"),
            ("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_INSECURE"),
            ("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_INSECURE"),
        ]
        for endpoint_var, insecure_var in endpoint_vars:
            endpoint = os.environ.get(endpoint_var, "")
            if endpoint.startswith("http://"):
                os.environ.setdefault(insecure_var, "true")

    def _attach_parent_context(self) -> None:
        """Extract TRACEPARENT from environment and attach to current context."""
        traceparent = os.environ.get("TRACEPARENT")
        if not traceparent:
            logger.debug("No TRACEPARENT found in environment")
            return

        logger.debug("Found TRACEPARENT: %s", traceparent)

        # Check if we already have a valid span context
        current_span = trace.get_current_span()
        if current_span.get_span_context().is_valid:
            logger.debug("Valid span context already exists, skipping TRACEPARENT extraction")
            return

        # Extract and attach the parent context
        ctx = propagate.extract({"traceparent": traceparent})
        context.attach(ctx)
        logger.debug("Attached parent context from TRACEPARENT")

    def _init_tracer(self) -> None:
        """Initialize the tracer provider with OTLP exporter."""
        self._tracer_provider = sdktrace.TracerProvider()

        exporter = _get_otlp_exporter()
        if exporter:
            processor = BatchSpanProcessor(exporter)
            self._tracer_provider.add_span_processor(processor)
            logger.debug("Added OTLP span exporter")

        trace.set_tracer_provider(self._tracer_provider)

    def _init_logger(self) -> None:
        """Initialize the logger provider with OTLP exporter."""
        self._logger_provider = sdklogs.LoggerProvider()

        exporter = _get_otlp_log_exporter()
        if exporter:
            processor = BatchLogRecordProcessor(exporter)
            self._logger_provider.add_log_record_processor(processor)
            logger.debug("Added OTLP log exporter")

        set_logger_provider(self._logger_provider)

    def get_tracer(self) -> trace.Tracer:
        """Get a tracer for pytest instrumentation."""
        self.configure()
        return trace.get_tracer("io.dagger.pytest", __import__("pytest_otel").__version__)

    def get_logger(self) -> sdklogs.Logger:
        """Get a logger for pytest instrumentation."""
        self.configure()
        if self._logger_provider:
            return self._logger_provider.get_logger("io.dagger.pytest")
        return sdklogs.get_logger_provider().get_logger("io.dagger.pytest")

    def shutdown(self) -> None:
        """Flush and shutdown providers."""
        with self._lock:
            if self._tracer_provider:
                self._tracer_provider.force_flush()
                self._tracer_provider.shutdown()
                self._tracer_provider = None
                logger.debug("Tracer provider shut down")

            if self._logger_provider:
                self._logger_provider.force_flush()
                self._logger_provider.shutdown()
                self._logger_provider = None
                logger.debug("Logger provider shut down")

            self._is_configured = False


# Global singleton instance
_config = TelemetryConfig()


def configure() -> None:
    """Configure OpenTelemetry for pytest."""
    _config.configure()


def get_tracer() -> trace.Tracer:
    """Get a tracer for pytest instrumentation."""
    return _config.get_tracer()


def get_logger() -> sdklogs.Logger:
    """Get a logger for pytest instrumentation."""
    return _config.get_logger()


def shutdown() -> None:
    """Shutdown OpenTelemetry providers."""
    _config.shutdown()
