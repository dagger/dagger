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

@env.function
async def build() -> dagger.Container:
    """ TODO: this should be an artifact, but function works for now """
    base = await dagger.apko().wolfi(["python-3.11", "py3.11-pip"])
    entrypoint = await (
        base
        .with_exec(["pip", "install", "shiv"])
        .with_mounted_directory("/src", dagger.host().directory("."))
        .with_exec(["shiv", "-e", "src.client.main:main", "-o", "/entrypoint", "/src/universe/_demo/client", "--root", "/tmp/.shiv"])
        .file("/entrypoint")
    )
    return await base.with_file("/entrypoint", entrypoint).with_entrypoint(["/entrypoint"])
