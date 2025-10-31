from opentelemetry import trace
import datetime

from dagger import dag, function, object_type

tracer = trace.get_tracer(__name__)

now = str(datetime.datetime.now())


@object_type
class Python:
    @function
    async def echo(self, msg: str) -> str:
        return await (
            dag.container().from_("alpine:latest").with_exec(["echo", msg]).stdout()
        )

    @function
    async def custom_span(self) -> str:
        with tracer.start_as_current_span("custom span"):
            return await self.echo(f"hello from Python! it is currently {now}")

    @function
    async def pending(self):
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_env_variable("NOW", now)
            .with_exec(["sleep", "1"])
            .with_exec(["false"])
            .with_exec(["sleep", "1"])
            .sync()
        )
