"""OpenTelemetry logging handler for pytest.

Captures Python logging records and forwards them to OpenTelemetry
as log records, associating them with the current trace context.
"""

import logging
from typing import Optional

from opentelemetry import context, trace
from opentelemetry._logs import SeverityNumber
from opentelemetry.sdk._logs import LogRecord

from pytest_otel.config import get_logger

# Mapping from Python logging levels to OpenTelemetry severity
_SEVERITY_MAP = {
    logging.DEBUG: SeverityNumber.DEBUG,
    logging.INFO: SeverityNumber.INFO,
    logging.WARNING: SeverityNumber.WARN,
    logging.ERROR: SeverityNumber.ERROR,
    logging.CRITICAL: SeverityNumber.FATAL,
}


def _get_severity(level: int) -> SeverityNumber:
    """Map Python log level to OpenTelemetry severity."""
    if level >= logging.CRITICAL:
        return SeverityNumber.FATAL
    if level >= logging.ERROR:
        return SeverityNumber.ERROR
    if level >= logging.WARNING:
        return SeverityNumber.WARN
    if level >= logging.INFO:
        return SeverityNumber.INFO
    return SeverityNumber.DEBUG


class OtelLogHandler(logging.Handler):
    """Logging handler that forwards logs to OpenTelemetry.

    This handler captures Python logging records and emits them as
    OpenTelemetry log records, preserving the trace context so logs
    are associated with the correct spans.
    """

    def __init__(self, level: int = logging.NOTSET) -> None:
        super().__init__(level)
        self._otel_logger: Optional[object] = None

    def emit(self, record: logging.LogRecord) -> None:
        """Emit a log record to OpenTelemetry."""
        try:
            # Get the OpenTelemetry logger lazily
            if self._otel_logger is None:
                self._otel_logger = get_logger()

            # Format the message
            msg = self.format(record)

            # Build attributes from log record
            attributes = {
                "log.logger": record.name,
                "log.level": record.levelname,
            }

            if record.pathname:
                attributes["code.filepath"] = record.pathname
            if record.lineno:
                attributes["code.lineno"] = record.lineno
            if record.funcName:
                attributes["code.function"] = record.funcName

            # Add exception info if present
            if record.exc_info and record.exc_info[1]:
                exc = record.exc_info[1]
                attributes["exception.type"] = type(exc).__name__
                attributes["exception.message"] = str(exc)

            # Emit the log record
            self._otel_logger.emit(
                LogRecord(
                    timestamp=int(record.created * 1e9),  # Convert to nanoseconds
                    observed_timestamp=int(record.created * 1e9),
                    severity_text=record.levelname,
                    severity_number=_get_severity(record.levelno),
                    body=msg,
                    resource=getattr(self._otel_logger, "resource", None),
                    attributes=attributes,
                    context=context.get_current(),
                )
            )

        except Exception:
            # Don't let logging errors break tests
            self.handleError(record)


class CapturedOutputHandler:
    """Handler for capturing pytest's captured stdout/stderr.

    This is used to send captured test output to OpenTelemetry as log events
    on the test span.
    """

    @staticmethod
    def record_output(stdout: str, stderr: str) -> None:
        """Record captured stdout/stderr on the current span."""
        span = trace.get_current_span()
        if not span.is_recording():
            return

        if stdout:
            span.add_event(
                "stdout",
                attributes={
                    "stream": "stdout",
                    "content": stdout[:4096],  # Limit size
                },
            )

        if stderr:
            span.add_event(
                "stderr",
                attributes={
                    "stream": "stderr",
                    "content": stderr[:4096],  # Limit size
                },
            )
