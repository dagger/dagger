"""The Python SDK's development module."""

import dataclasses
import logging
from typing import Annotated

import dagger
from dagger import Doc, dag, field, function, object_type
from dagger.log import configure_logging
from main.consts import PYTHON_VERSION
from main.debug import Debug
from main.docs import Docs
from main.rye import Rye
from main.test import Test
from main.utils import mounted_workdir, python_base

configure_logging(logging.DEBUG)


def dev_base() -> dagger.Container:
    return (
        python_base()
        .with_env_variable("RUFF_CACHE_DIR", "/root/.cache/ruff")
        .with_env_variable("MYPY_CACHE_DIR", "/root/.cache/mypy")
        .with_mounted_cache(
            "/root/.cache/ruff",
            dag.cache_volume("modpythondev-ruff"),
        )
        .with_mounted_cache(
            "/root/.cache/mypy",
            dag.cache_volume(f"modpythondev-mypy-{PYTHON_VERSION}"),
        )
        .with_file(
            "/opt/requirements.txt",
            dag.current_module().source().file("requirements-dev.lock"),
        )
        .with_exec(["sed", "-i", "/-e file:/d", "/opt/requirements.txt"])
        .with_exec(
            [
                "uv",
                "pip",
                "install",
                "--no-deps",
                "--compile",
                "--strict",
                "-r",
                "/opt/requirements.txt",
            ]
        )
    )


@object_type
class PythonSdkDev:
    """The Python SDK's development module."""

    # TODO: require sdk's dir and mount in /src/sdk/python to match file structure
    # TODO: install requirements-dev.lock from sdk dir

    src: Annotated[
        dagger.Directory,
        Doc("Directory with sources"),
    ] = field(default=dag.directory)

    container: Annotated[dagger.Container, Doc("Base container")] = dataclasses.field(
        default_factory=dev_base,
        init=False,
    )

    def __post_init__(self):
        self.container = self.container.with_(mounted_workdir(self.src)).with_exec(["uv", "pip", "install", "--no-deps", "-e", "."])

    @function
    def rye(self) -> Rye:
        return Rye(container=self.container)

    @function
    def debug(self) -> Debug:
        return Debug(container=self.container)

    @function
    async def lint(
        self,
        paths: Annotated[
            tuple[str, ...],
            Doc("List of files or directories to check"),
        ] = (),
    ) -> str:
        # TODO: default to self.src but optionally mount/run from root of repo
        return await (
            self.container.with_exec(["ruff", "check", *paths])
            .with_exec(["ruff", "format", "--check", "--diff", *paths])
            .stdout()
        )

    @function
    def fmt(
        self,
        paths: Annotated[
            tuple[str, ...],
            Doc("List of files or directories to check"),
        ] = (),
    ) -> dagger.Directory:
        ctr = self.container.with_exec(["ruff", "check", "--fix-only", *paths]).with_exec(["ruff", "format", *paths])
        return self.src.diff(ctr.directory("/work"))

    @function
    async def typing(self) -> str:
        """Run the type checker (mypy)."""
        return await self.container.with_exec(["mypy", "."]).stdout()

    @function
    def test(self) -> Test:
        """Run the test suite."""
        # TODO: allow testing different with Python versions, but only here.
        return Test(container=self.container)

    @function
    def docs(self) -> Docs:
        return Docs(container=self.container)

