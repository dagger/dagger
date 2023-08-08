import dagger
from dagger.server import Environment


env = Environment()


# TODO: this should be a command
@env.check
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

