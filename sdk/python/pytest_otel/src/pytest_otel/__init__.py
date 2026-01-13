"""Pytest plugin for OpenTelemetry instrumentation with Dagger.

This plugin automatically instruments pytest tests with OpenTelemetry spans,
enabling test visibility in Dagger's TUI and Dagger Cloud.

The plugin is automatically loaded when installed via the pytest11 entry point.
No configuration is required - it extracts TRACEPARENT from the environment
and creates spans for each test.
"""

__version__ = "0.1.0"

# Plugin hooks are exported from plugin.py and auto-discovered by pytest
# via the entry point in pyproject.toml
