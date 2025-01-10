import anyio
from dagger import dag, function, object_type
from opentelemetry import trace

tracer = trace.get_tracer(__name__)


@object_type
class MyModule:
    @function
    async def foo(self):
        # clone the source code repository
        source = dag.git("https://github.com/dagger/hello-dagger").branch("main").tree()

        # set up a container with the source code mounted
        # install dependencies
        container = (
            dag.container()
            .from_("node:latest")
            .with_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
        )

        # run tasks concurrently
        # emit a span for each
        async with anyio.create_task_group() as tg:
            tg.start_soon(self._lint, container)
            tg.start_soon(self._typecheck, container)
            tg.start_soon(self._format, container)
            tg.start_soon(self._test, container)

    async def _lint(self, container):
        with tracer.start_as_current_span("lint code"):
            await container.with_exec(["npm", "run", "lint"]).sync()

    async def _typecheck(self, container):
        with tracer.start_as_current_span("check types"):
            result = await container.with_exec(["npm", "run", "type-check"]).sync()
            if result.exit_code != 0:
                raise Exception(
                    f"Type checking failed with exit code {result.exit_code}"
                )

    async def _format(self, container):
        with tracer.start_as_current_span("format code"):
            result = await container.with_exec(["npm", "run", "format"]).sync()
            if result.exit_code != 0:
                raise Exception(
                    f"Code formatting failed with exit code {result.exit_code}"
                )

    async def _test(self, container):
        with tracer.start_as_current_span("run unit tests"):
            result = await container.with_exec(
                ["npm", "run", "test:unit", "run"]
            ).sync()
            if result.exit_code != 0:
                raise Exception(f"Tests failed with exit code {result.exit_code}")
