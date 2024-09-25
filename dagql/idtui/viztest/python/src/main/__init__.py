"""A generated module for Python functions

This module has been generated via dagger init and serves as a reference to
basic module structure as you get started with Dagger.

Two functions have been pre-created. You can modify, delete, or add to them,
as needed. They demonstrate usage of arguments and return types using simple
echo and grep commands. The functions can be called from the dagger CLI or
from one of the SDKs.

The first line in this comment block is a short description line and the
rest is a long description with more detail on the module's purpose or usage,
if appropriate. All modules should have a short description.
"""

# NOTE: it's recommended to move your code into other files in this package
# and keep __init__.py for imports only, according to Python's convention.
# The only requirement is that Dagger needs to be able to import a package
# called "main" (i.e., src/main/).
#
# For example, to import from src/main/main.py:
# >>> from .main import Python as Python

from opentelemetry import trace
import datetime

from dagger import dag, function, object_type

tracer = trace.get_tracer(__name__)


@object_type
class Python:
    @function
    async def echo(self, msg: str) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["echo", msg])
            .stdout()
        )

    @function
    async def custom_span(self) -> str:
        with tracer.start_as_current_span("custom span"):
            return await self.echo("hey dude!")

    @function
    async def pending(self):
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_env_variable("NOW", str(datetime.datetime.now()))
            .with_exec(["sleep", "1"])
            .with_exec(["false"])
            .with_exec(["sleep", "1"])
            .sync()
        )
