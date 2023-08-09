import dagger
from dagger.server import Environment


env = Environment()


@env.command
async def publish() -> str:
    """Publish the client"""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )

@env.check
async def unit_test() -> str:
    """Run unit tests"""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )

