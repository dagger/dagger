import dagger
from dagger.server import Environment


env = Environment()


@env.command
async def pypublish() -> str:
    """Publish the client."""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )


@env.check
async def pylint() -> str:
    """Lint the Python SDK"""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )

