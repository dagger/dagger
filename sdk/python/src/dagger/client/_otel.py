import contextlib
import logging
import os

import httpx
from opentelemetry import context, propagate, trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.semconv.resource import ResourceAttributes
from opentelemetry.trace import propagation

logger = logging.getLogger(__name__)


class AsyncTransport(httpx.AsyncHTTPTransport):
    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        # Get traceparent from request headers if present.
        propagate.inject(request.headers)
        return await super().handle_async_request(request)


provider = TracerProvider(
    resource=Resource.create(
        attributes={ResourceAttributes.SERVICE_NAME: "dagger-python-sdk"},
        schema_url=ResourceAttributes.SCHEMA_URL,
    ),
)
provider.add_span_processor(
    BatchSpanProcessor(OTLPSpanExporter(insecure=True)),
)

trace.set_tracer_provider(provider)

tracer = trace.get_tracer("dagger.io/sdk.python")


def get_context() -> context.Context:
    ctx = context.get_current()

    if propagation.get_current_span(ctx).get_span_context().is_valid:
        return ctx

    if p := os.getenv("TRACEPARENT"):
        logger.debug("Falling back to $TRACEPARENT: %s", p)
        return propagate.extract({"traceparent": p}, context=ctx)

    return ctx


@contextlib.asynccontextmanager
async def start_as_current_span(name: str):
    with tracer.start_as_current_span(
        name,
        context=get_context(),
        kind=trace.SpanKind.CLIENT,
        attributes={
            # In effect, the following two attributes hide the exec /runtime span.
            #
            # Replace the parent span,
            "dagger.io/ui.mask": True,
            # and only show our children.
            "dagger.io/ui.passthrough": True,
        },
    ) as span:
        yield span
