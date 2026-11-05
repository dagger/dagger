import json
import traceback
from types import SimpleNamespace

import gql
import pytest
from opentelemetry.environment_variables import OTEL_TRACES_EXPORTER
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import (
    InMemorySpanExporter,
)
from opentelemetry.semconv.attributes.exception_attributes import (
    EXCEPTION_MESSAGE,
    EXCEPTION_STACKTRACE,
    EXCEPTION_TYPE,
)

from dagger import telemetry
from dagger._exceptions import QueryError, QueryErrorValue
from dagger.mod import Module, _exceptions
from dagger.mod._exceptions import FunctionError

pytestmark = [
    pytest.mark.anyio,
]

MSG = "sku a1"
WRAPPER = "dagger.mod._exceptions.FunctionError"


class Inventory:
    class OutOfStockError(Exception):
        pass


class InventoryFunctionError(FunctionError):
    pass


OUT_OF_STOCK = f"{__name__}.Inventory.OutOfStockError"


def check_stock():
    raise Inventory.OutOfStockError(MSG)


mod = Module()
mod.log_exceptions = False


@mod.object_type
class Shop:
    @mod.function
    def out_of_stock(self) -> str:
        raise Inventory.OutOfStockError(MSG)

    @mod.function
    async def async_out_of_stock(self) -> str:
        raise Inventory.OutOfStockError(MSG)

    @mod.function
    def builtin_error(self) -> str:
        raise ValueError(MSG)

    @mod.function
    def chained_domain_error(self) -> str:
        try:
            {}["a1"]
        except KeyError as e:
            raise Inventory.OutOfStockError(MSG) from e

    @mod.function
    def function_error(self) -> str:
        raise FunctionError(MSG)

    @mod.function
    async def missing_await(self) -> str:
        return self.async_out_of_stock()

    @mod.function
    def function_error_subclass(self) -> str:
        raise InventoryFunctionError(MSG) from Inventory.OutOfStockError(MSG)

    @mod.function
    def implicit_context(self) -> str:
        try:
            check_stock()
        except Inventory.OutOfStockError:
            raise FunctionError(MSG)  # noqa: B904

    @mod.function
    def extra(self) -> str:
        raise FunctionError(MSG, extra={"sku": "a1"}) from Inventory.OutOfStockError(
            MSG
        )

    @mod.function
    def query_error(self) -> str:
        raise QueryError(
            [QueryErrorValue("query failed", extensions={"code": "NOT_FOUND"})],
            gql.GraphQLRequest("query { shop }"),
        )


@pytest.fixture(autouse=True)
def setup_telemetry(monkeypatch: pytest.MonkeyPatch):
    # Shadowing the session fixture skips telemetry.initialize(). The exporter
    # setting guards against export if a later code path initializes it.
    monkeypatch.setenv(OTEL_TRACES_EXPORTER, "none")


@pytest.fixture
def exporter(monkeypatch: pytest.MonkeyPatch):
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    monkeypatch.setattr(telemetry, "get_tracer", lambda: provider.get_tracer(__name__))
    yield exporter
    provider.shutdown()


@pytest.fixture
def returned_errors(monkeypatch: pytest.MonkeyPatch):
    errors = []

    class Error:
        def __init__(self, message: str):
            self.message = message
            self.values = {}

        def with_value(self, key: str, value: str):
            self.values[key] = value
            return self

    class FunctionCall:
        async def return_error(self, error: Error):
            errors.append(error)

    monkeypatch.setattr(
        _exceptions,
        "dag",
        SimpleNamespace(error=Error, current_function_call=FunctionCall),
    )
    return errors


async def raised(name: str) -> Exception:
    with pytest.raises((FunctionError, QueryError)) as exc_info:
        await mod.get_result("Shop", {}, name, {})
    return exc_info.value


def wrapper_event(exc: Exception):
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    with provider.get_tracer(__name__).start_as_current_span("baseline") as span:
        span.record_exception(exc)
    (baseline,) = exporter.get_finished_spans()
    provider.shutdown()
    (event,) = baseline.events
    return baseline, event


async def record(exc: Exception, exporter: InMemorySpanExporter, returned_errors):
    await _exceptions.record_exception(exc)

    (span,) = exporter.get_finished_spans()
    assert span.name == "recording Python exception"
    assert dict(span.attributes) == {"dagger.io/ui.internal": True}
    assert len(span.events) == 1
    (event,) = span.events
    assert event.name == "exception"

    baseline, baseline_event = wrapper_event(exc)
    assert span.status.status_code == baseline.status.status_code
    assert event.attributes[EXCEPTION_STACKTRACE] == "".join(
        traceback.format_exception(exc)
    )
    assert {k: v for k, v in event.attributes.items() if k != EXCEPTION_TYPE} == {
        k: v for k, v in baseline_event.attributes.items() if k != EXCEPTION_TYPE
    }

    (error,) = returned_errors
    return event, baseline_event, error


def payload(exc: Exception, exc_type: str, **values):
    return (
        f"{exc_type}: {exc}",
        {
            **values,
            EXCEPTION_TYPE: json.dumps(exc_type),
            EXCEPTION_MESSAGE: json.dumps(str(exc)),
            EXCEPTION_STACKTRACE: json.dumps("".join(traceback.format_exception(exc))),
        },
    )


@pytest.mark.parametrize(
    ("name", "cause_type", "chained"),
    [
        pytest.param("out_of_stock", OUT_OF_STOCK, [], id="sync"),
        pytest.param("async_out_of_stock", OUT_OF_STOCK, [], id="async"),
        pytest.param("builtin_error", "ValueError", [], id="builtin"),
        pytest.param(
            "chained_domain_error",
            OUT_OF_STOCK,
            ["KeyError: 'a1'"],
            id="domain-chained",
        ),
    ],
)
async def test_wrapper_event_records_cause_type(
    name, cause_type, chained, exporter, returned_errors
):
    exc = await raised(name)
    assert type(exc) is FunctionError

    event, baseline_event, error = await record(exc, exporter, returned_errors)

    assert baseline_event.attributes[EXCEPTION_TYPE] == WRAPPER
    assert event.attributes[EXCEPTION_TYPE] == cause_type
    assert event.attributes[EXCEPTION_MESSAGE] == MSG
    stacktrace = event.attributes[EXCEPTION_STACKTRACE]
    for line in [
        *chained,
        f"{cause_type}: {MSG}",
        "The above exception was the direct cause of the following exception:",
        f"{WRAPPER}: {MSG}",
    ]:
        assert line in stacktrace
    assert (error.message, error.values) == payload(exc, "FunctionError")


@pytest.mark.parametrize(
    ("name", "exc_type"),
    [
        pytest.param("function_error", "FunctionError", id="cause-less"),
        pytest.param("missing_await", "FunctionError", id="missing-await"),
        pytest.param(
            "function_error_subclass",
            f"{__name__}.InventoryFunctionError",
            id="subclass",
        ),
        pytest.param("implicit_context", "FunctionError", id="implicit-context"),
    ],
)
async def test_event_keeps_wrapper_type(name, exc_type, exporter, returned_errors):
    exc = await raised(name)

    event, baseline_event, error = await record(exc, exporter, returned_errors)

    assert event.attributes == baseline_event.attributes
    assert (error.message, error.values) == payload(exc, exc_type)


async def test_extra_wrapper_event_records_cause_type(exporter, returned_errors):
    exc = await raised("extra")

    event, _, error = await record(exc, exporter, returned_errors)

    assert event.attributes[EXCEPTION_TYPE] == OUT_OF_STOCK
    assert (error.message, error.values) == payload(
        exc, "FunctionError", **{"extra.sku": json.dumps("a1")}
    )


async def test_query_error_keeps_extensions(exporter, returned_errors):
    exc = await raised("query_error")
    assert type(exc) is QueryError

    event, baseline_event, error = await record(exc, exporter, returned_errors)

    assert event.attributes == baseline_event.attributes
    _, expected_values = payload(exc, "dagger.QueryError")
    assert error.message == "query failed"
    assert error.values == {**expected_values, "code": json.dumps("NOT_FOUND")}
