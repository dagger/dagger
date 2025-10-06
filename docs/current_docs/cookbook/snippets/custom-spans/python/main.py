import anyio
from opentelemetry import trace

from dagger import dag, function, object_type

tracer = trace.get_tracer(__name__)


@object_type
class MyModule:
    @function
    async def foo(self):
        # clone the source code repository
        source = dag.git("https://github.com/dagger/hello-dagger").branch("main").tree()

        # list versions to test against
        versions = ["20", "22", "23"]

        async def _test(version: str):
            with tracer.start_as_current_span(
                f"running unit tests with Node {version}"
            ):
                await (
                    dag.container()
                    .from_(f"node:{version}")
                    .with_directory("/src", source)
                    .with_workdir("/src")
                    .with_exec(["npm", "install"])
                    .with_exec(["npm", "run", "test:unit", "run"])
                    .sync()
                )

        # run tests concurrently
        # emit a span for each
        async with anyio.create_task_group() as tg:
            for version in versions:
                tg.start_soon(_test, version)
