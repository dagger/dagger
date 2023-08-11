from typing import Annotated
import dagger
from dagger.server import Environment


env = Environment()


@env.command
async def publish(version: Annotated[str, "The version to publish."]) -> str:
    """Publish the client"""
    if version == "nope":
        print("OH NO! Publishing the client failed!")
        raise RuntimeError("Publishing failed")

    return f"Published version {version}"

@env.check
async def unit_test() -> str:
    """Run unit tests"""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )

