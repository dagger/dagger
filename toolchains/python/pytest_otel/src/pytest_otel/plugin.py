"""Pytest plugin hooks for OpenTelemetry instrumentation.

This module provides the pytest hooks that automatically instrument tests
with OpenTelemetry spans. The plugin is auto-discovered via the pytest11
entry point defined in pyproject.toml.
"""

import logging
from typing import Generator, Optional

import pytest
from _pytest.reports import TestReport

from pytest_otel import config as otel_config
from pytest_otel import tracer
from pytest_otel.logging_handler import OtelLogHandler

logger = logging.getLogger(__name__)

# Track whether plugin is enabled
_enabled: bool = False
_log_handler: Optional[OtelLogHandler] = None


def pytest_addoption(parser: pytest.Parser) -> None:
    """Add plugin command-line options."""
    group = parser.getgroup("opentelemetry", "OpenTelemetry instrumentation")
    group.addoption(
        "--no-otel",
        action="store_true",
        default=False,
        help="Disable OpenTelemetry instrumentation",
    )


def pytest_configure(config: pytest.Config) -> None:
    """Initialize OpenTelemetry on pytest startup."""
    global _enabled, _log_handler

    # Check if explicitly disabled
    if config.option.no_otel:
        logger.debug("OpenTelemetry instrumentation disabled via --no-otel")
        return

    # Initialize telemetry
    try:
        otel_config.configure()
        _enabled = True
        logger.debug("OpenTelemetry instrumentation enabled")

        # Set up logging capture
        _log_handler = OtelLogHandler()
        # Add to root logger to capture all test logs
        root_logger = logging.getLogger()
        root_logger.addHandler(_log_handler)

    except Exception as e:
        logger.warning("Failed to initialize OpenTelemetry: %s", e)
        _enabled = False


def pytest_unconfigure(config: pytest.Config) -> None:
    """Cleanup OpenTelemetry on pytest shutdown."""
    global _enabled, _log_handler

    if not _enabled:
        return

    # Remove log handler
    if _log_handler:
        root_logger = logging.getLogger()
        root_logger.removeHandler(_log_handler)
        _log_handler = None

    # Shutdown telemetry (flushes spans)
    try:
        otel_config.shutdown()
        logger.debug("OpenTelemetry instrumentation shut down")
    except Exception as e:
        logger.warning("Error shutting down OpenTelemetry: %s", e)

    _enabled = False


def pytest_sessionstart(session: pytest.Session) -> None:
    """Start session span when pytest session begins."""
    if not _enabled:
        return

    try:
        tracer.start_session(session)
        logger.debug("Started session span")
    except Exception as e:
        logger.warning("Failed to start session span: %s", e)


def pytest_sessionfinish(session: pytest.Session, exitstatus: int) -> None:
    """End session span when pytest session finishes."""
    if not _enabled:
        return

    try:
        tracer.end_session(exitstatus)
        logger.debug("Ended session span with exit status %d", exitstatus)
    except Exception as e:
        logger.warning("Failed to end session span: %s", e)


@pytest.hookimpl(wrapper=True, tryfirst=True)
def pytest_runtest_protocol(
    item: pytest.Item, nextitem: Optional[pytest.Item]
) -> Generator[None, object, object]:
    """Wrap entire test execution in a span.

    This hook wraps the full test lifecycle (setup, call, teardown)
    ensuring all phases are captured in a single span.
    """
    if not _enabled:
        return (yield)

    # Start test span
    try:
        tracer.start_test(item)
    except Exception as e:
        logger.warning("Failed to start test span for %s: %s", item.nodeid, e)
        return (yield)

    # Execute test and capture outcome
    outcome = "passed"
    try:
        result = yield

        # Extract outcome from test reports
        outcome = _extract_outcome(result)

        return result
    except Exception as e:
        outcome = "error"
        tracer.record_exception(item, e)
        raise
    finally:
        # Always end span
        try:
            tracer.end_test(item, outcome)
        except Exception as e:
            logger.warning("Failed to end test span for %s: %s", item.nodeid, e)


@pytest.hookimpl(wrapper=True)
def pytest_runtest_makereport(
    item: pytest.Item, call: pytest.CallInfo
) -> Generator[None, TestReport, TestReport]:
    """Capture test reports and record exceptions."""
    report: TestReport = yield

    if not _enabled:
        return report

    # Record exceptions from the call phase
    if call.excinfo and report.when == "call":
        try:
            tracer.record_exception(item, call.excinfo.value)
        except Exception as e:
            logger.warning("Failed to record exception for %s: %s", item.nodeid, e)

    return report


def _extract_outcome(result: object) -> str:
    """Extract final test outcome from pytest reports.

    The result from pytest_runtest_protocol contains TestReport objects
    from setup, call, and teardown phases. We check ALL phases and return
    the worst outcome (failed > skipped > passed).
    """
    if result is None:
        return "passed"

    # Result should be iterable of reports
    try:
        reports = list(result) if hasattr(result, "__iter__") else [result]
    except (TypeError, ValueError):
        return "passed"

    # Check all phases - worst outcome wins
    call_outcome = "passed"

    for report in reports:
        if isinstance(report, TestReport):
            # Any failure in any phase means test failed
            if report.failed:
                return "failed"

            # Track call phase outcome specifically
            if report.when == "call":
                call_outcome = report.outcome

    return call_outcome
