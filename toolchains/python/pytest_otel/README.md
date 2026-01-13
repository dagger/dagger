# pytest-otel

A pytest plugin for OpenTelemetry instrumentation with Dagger.

This plugin automatically instruments pytest tests with OpenTelemetry spans, enabling test visibility in Dagger's TUI and Dagger Cloud.

## Features

- **Zero configuration**: Automatically creates spans for each test
- **TRACEPARENT propagation**: Inherits trace context from Dagger
- **Session spans**: Creates a parent span for the entire test session
- **Test hierarchy**: Captures module, class, and function information
- **Outcome tracking**: Records test pass/fail/skip status
- **Log capture**: Forwards Python logs to OpenTelemetry
- **Exception recording**: Records test exceptions on spans

## Installation

```bash
pip install pytest-otel
```

## Usage

Once installed, the plugin is automatically enabled. No code changes are required in your tests.

### With Dagger

When running tests through Dagger, the `TRACEPARENT` environment variable is automatically set, and test spans will appear in the Dagger TUI and Dagger Cloud.

```python
# In your Dagger module
@function
async def test(self, source: dagger.Directory) -> str:
    return await (
        dag.container()
        .from_("python:3.11-slim")
        .with_directory("/src", source)
        .with_workdir("/src")
        .with_exec(["pip", "install", "pytest", "pytest-otel"])
        .with_exec(["pytest", "-v"])
        .stdout()
    )
```

### Standalone

You can also use the plugin standalone with any OTLP-compatible backend:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
export TRACEPARENT="00-abc123...-def456...-01"  # Optional parent trace
pytest
```

### Disabling

To disable instrumentation:

```bash
pytest --no-otel
```

## Span Attributes

Each test span includes the following attributes:

| Attribute | Description |
|-----------|-------------|
| `dagger.io/ui.boundary` | Prevents log bubbling in Dagger TUI |
| `dagger.io/ui.reveal` | Shows top-level tests in Dagger TUI |
| `pytest.nodeid` | Full pytest node ID |
| `pytest.module` | Test module path |
| `pytest.class` | Test class name (if applicable) |
| `pytest.function` | Test function name |
| `pytest.outcome` | Test result (passed/failed/skipped) |

## Development

```bash
cd sdk/python/pytest_otel
pip install -e ".[dev]"
pytest
```

## License

Apache-2.0
