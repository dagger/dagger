"""The Python SDK's development module."""
import logging
import uuid
from collections.abc import Sequence
from typing import Annotated

import anyio

import dagger
from dagger import Doc, dag, function
from dagger.log import configure_logging

from .consts import DEP_ENVS
from .deps import Deps, Hatch
from .docs import Docs
from .lint import Lint
from .test import Test
from .utils import (
    from_host,
    mounted_workdir,
    python_base,
    sdk,
)

configure_logging(logging.DEBUG)


# Top-level constructors to module objects.
function(Deps)
function(Docs)
function(Lint)
function(Test)


@function
def all_deps() -> list[Deps]:
    """Manage the SDK's development dependencies."""
    # EXPERIMENTAL: Testing the ability to call function on multiple objects
    # instead of using a concurrent task group.
    return [Deps(env=env) for env in DEP_ENVS]


@function
def lock(
    src: Annotated[
        dagger.Directory | None,
        Doc("Directory with the current pinned requirements/*.txt files"),
    ] = None,
) -> dagger.Directory:
    """Update all of the SDK's pinned development dependencies."""
    if src is None:
        src = from_host(["requirements/*.txt"])
    dist = src
    for env in DEP_ENVS:
        dist = dist.with_file(f"requirements/{env}.txt", Deps(env=env).lock())
    return src.diff(dist)


@function
def publish(
    version: Annotated[str, Doc("The SDK version to publish")],
    token: Annotated[dagger.Secret, Doc("The PyPI token to use")],
) -> dagger.Container:
    """Publish the SDK library to PyPI under dagger-io."""
    hatch = Hatch()
    artifacts = hatch.build(from_host(), version.removeprefix("sdk/python/v"))
    return hatch.publish(artifacts, token)


@function
async def tests(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
    versions: Sequence[str] = ("3.10", "3.11"),
) -> bool:
    """Run the test suite in supported Python versions (using concurrency)."""
    async with anyio.create_task_group() as tg:
        for test in all_tests(src, requirements, versions):

            async def run(test: Test):
                await test.default()

            tg.start_soon(run, test)
    return True


@function
def all_tests(
    src: dagger.Directory | None = None,
    requirements: dagger.File | None = None,
    versions: Sequence[str] = ("3.10", "3.11"),
) -> list[Test]:
    """Run the test suite in supported Python versions."""
    # EXPERIMENTAL: Seeing if this can replace the task group.
    kwargs = {}
    if src is not None:
        kwargs["src"] = src
    if requirements is not None:
        kwargs["requirements"] = requirements
    return [Test(version=version, **kwargs) for version in versions]


@function
def script(
    file: Annotated[dagger.File, Doc("The .py file to execute")]
) -> dagger.Container:
    """Run a Python script."""
    return (
        python_base()
        .with_(sdk)
        .with_(
            mounted_workdir(
                dag.directory().with_file("main.py", file),
            ),
        )
        .with_focus()
        .with_exec(
            ["python", "main.py"],
            experimental_privileged_nesting=True,
        )
    )


@function
async def joke() -> str:
    """Tell me a joke."""
    return await (
        python_base()
        .with_exec(["pip", "install", "pyjokes"])
        .with_env_variable("CACHE_BUSTER", uuid.uuid4().hex)
        .with_exec(["pyjoke"])
        .stdout()
    )
