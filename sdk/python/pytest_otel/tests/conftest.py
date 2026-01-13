"""Test configuration for pytest-otel tests."""

import pytest


@pytest.fixture
def reset_telemetry():
    """Reset telemetry state between tests."""
    from pytest_otel import config

    yield

    # Reset the singleton state (thread-safe access)
    with config._config._lock:
        config._config._is_configured = False
        config._config._tracer_provider = None
        config._config._logger_provider = None
