"""Tests for the config module."""

import os

import pytest


class TestTelemetryConfig:
    """Tests for TelemetryConfig."""

    def test_singleton(self):
        """Verify TelemetryConfig is a singleton."""
        from pytest_otel.config import TelemetryConfig

        config1 = TelemetryConfig()
        config2 = TelemetryConfig()

        assert config1 is config2

    def test_configure_extracts_traceparent(self, monkeypatch, reset_telemetry):
        """Test that configure extracts TRACEPARENT from environment."""
        from opentelemetry import trace

        from pytest_otel.config import TelemetryConfig

        # Set a valid TRACEPARENT
        traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
        monkeypatch.setenv("TRACEPARENT", traceparent)

        config = TelemetryConfig()
        config._is_configured = False  # Reset state
        config.configure()

        # Verify context was attached (span context should be valid)
        span = trace.get_current_span()
        ctx = span.get_span_context()

        # The trace ID should match the TRACEPARENT
        assert ctx.trace_id == int("0af7651916cd43dd8448eb211c80319c", 16)

    def test_configure_without_traceparent(self, monkeypatch, reset_telemetry):
        """Test configuration without TRACEPARENT."""
        from pytest_otel.config import TelemetryConfig

        monkeypatch.delenv("TRACEPARENT", raising=False)

        config = TelemetryConfig()
        config._is_configured = False
        config.configure()

        # Should complete without error
        assert config._is_configured

    def test_configure_idempotent(self, reset_telemetry):
        """Test that configure is idempotent."""
        from pytest_otel.config import TelemetryConfig

        config = TelemetryConfig()
        config._is_configured = False

        config.configure()
        first_provider = config._tracer_provider

        config.configure()
        second_provider = config._tracer_provider

        # Should not create a new provider
        assert first_provider is second_provider

    def test_get_tracer_auto_configures(self, reset_telemetry):
        """Test that get_tracer automatically configures telemetry."""
        from pytest_otel.config import TelemetryConfig

        config = TelemetryConfig()
        config._is_configured = False

        tracer = config.get_tracer()

        assert config._is_configured
        assert tracer is not None
