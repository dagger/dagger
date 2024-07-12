"""The Python SDK's development module."""

import logging
import os
from collections.abc import Sequence
from typing import Annotated, Final

import anyio

import dagger
from dagger import Doc, dag, field, function, object_type, telemetry
from dagger.log import configure_logging
from main.consts import PYTHON_VERSION, SUPPORTED_VERSIONS
from main.debug import Debug
#from main.docs import Docs
#from main.test import TestSuite
from main.utils import mounted_workdir, python_base, venv

configure_logging(logging.DEBUG)

UV_IMAGE: Final[str] = os.getenv("DAGGER_UV_IMAGE", "ghcr.io/astral-sh/uv:latest")


@object_type
class PythonSdkDev:
    """The Python SDK's development module."""

    # TODO: require sdk's dir and mount in /src/sdk/python to match file structure
    # TODO: install requirements-dev.lock from sdk dir

    src: Annotated[
        dagger.Directory,
        Doc("Directory with sources")
    ]

    container: Annotated[
        dagger.Container,
        Doc("Container to run commands in")
    ] = field(init=False)

    def __post_init__(self):
        self.container = (
            dag.apko().wolfi(["libgcc"])
            .with_env_variable("PYTHONUNBUFFERED", "1")
            .with_env_variable("PATH", "/root/.local/bin:$PATH", expand=True)
            .with_(self.tools_cache("uv", "hatch", "ruff", "mypy"))
            .with_(self.uv)
            .with_(self.hatch)
            .with_workdir("/src/sdk/python")
            .with_mounted_directory("", self.src)
            .with_exec(["uv", "sync"])
        )

    def uv(self, ctr: dagger.Container) -> dagger.Container:
        return ctr.with_directory(
            "/root/.local/bin",
            dag.container().from_(UV_IMAGE).rootfs(),
            include=["uv*"]
        )

    def hatch(self, ctr: dagger.Container) -> dagger.Container:
        return ctr.with_exec(["uv", "tool", "install", "hatch==1.12.0"])

    def tools_cache(self, *args: str):
        def _tools(ctr: dagger.Container) -> dagger.Container:
            for tool in args:
                ctr = (
                    ctr
                    .with_mounted_cache(
                        f"/root/.cache/{tool}",
                        dag.cache_volume(f"modpythondev-{tool}"),
                    )
                    .with_env_variable(
                        f"{tool.upper()}_CACHE_DIR",
                        f"/root/.cache/{tool}",
                    )
                )
            return ctr
        return _tools

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
        # TODO: optionally mount/run from any dir
        return await (
            self.container
            .with_exec(["uv", "run", "ruff", "check", *paths])
            .with_exec(["uv", "run", "ruff", "format", "--check", "--diff", *paths])
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
        # TODO: optionally mount/run from any dir
        ctr = (
            self.container
            .with_exec(["uv", "run", "ruff", "check", "--fix-only", *paths])
            .with_exec(["uv", "run", "ruff", "format", *paths])
        )
        return self.src.diff(ctr.directory("/work"))

    '''
    @function
    async def typing(self) -> str:
        """Run the type checker (mypy)."""
        return await self.container.with_exec(["mypy", "."]).stdout()

    @function
    def test_suite(self, version: str | None = None) -> TestSuite:
        """Run the test suite."""
        ctr = self.container if version is None else python_base(version).with_(self.install)
        return TestSuite(container=ctr)

    @function
    def build(self) -> dagger.Directory:
        """Build Python SDK package for distribution."""
        return self.container.with_exec(["hatch", "build", "--clean"]).directory("dist")

    @function
    async def test(self, versions: Sequence[str] = SUPPORTED_VERSIONS):
        """Run the test suite on multiple Python versions."""
        tracer = telemetry.get_tracer()

        async def _test_suite(version: str):
            with tracer.start_as_current_span("Test suite"):
                await self.test_suite(version).default()

        async def _test_build(ctr: dagger.Container, dist: dagger.Directory, ext: str):
            with tracer.start_as_current_span(f"Test {ext} build"):
                await (
                    ctr
                    .with_mounted_directory("/dist", dist)
                    .with_exec(["sh", "-c", f"uv pip install /dist/*.{ext}"])
                    .with_exec(["python", "-c", "import dagger"])
                )

        async def _test_version(version: str):
            with tracer.start_as_current_span(f"Test Python {version}"):
                ctr = self.container if version is None else python_base(version)
                dist = self.build()

                async with anyio.create_task_group() as tg:
                    tg.start_soon(_test_suite, version)

                    for ext in ("tar.gz", "whl"):
                        tg.start_soon(_test_build, ctr, dist, ext)

        async with anyio.create_task_group() as tg:
            for version in versions:
                tg.start_soon(_test_version, version)

    @function
    def docs(self) -> Docs:
        return Docs(container=self.container)

    '''
