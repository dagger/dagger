import os
from typing import Annotated, Final, Literal, Self, get_args

import dagger
from dagger import DefaultPath, Doc, Ignore, dag, field, function, object_type

from .docs import Docs
from .test import TestSuite

UV_IMAGE: Final[str] = os.getenv("DAGGER_UV_IMAGE", "ghcr.io/astral-sh/uv:latest")
UV_VERSION: Final[str] = os.getenv("DAGGER_UV_VERSION", os.getenv("UV_VERSION", ""))
SUPPORTED_VERSIONS: Final = Literal["3.13", "3.12", "3.11", "3.10"]


@object_type
class PythonSdkDev:
    """The Python SDK's development module."""

    container: Annotated[
        dagger.Container,
        Doc("Container to run commands in"),
    ] = field()

    @classmethod
    def create(
        cls,
        source: Annotated[
            dagger.Directory,
            Doc("Directory with sources"),
            DefaultPath("/sdk/python"),
            Ignore(
                [
                    "*",
                    "!*.toml",
                    "!*.lock",
                    "!*/*.toml",
                    "!*/*.lock",
                    "!.python-version",
                    "!dev/src/**/*.py",
                    "!docs/**/*.py",
                    "!docs/**/*.rst",
                    "!runtime/Dockerfile",
                    "!src/**/*.py",
                    "!src/**/py.typed",
                    "!tests/**/*.py",
                    "!codegen/**/*.py",
                    "!README.md",
                    "!LICENSE",
                ]
            ),
        ],
        container: Annotated[
            dagger.Container | None,
            Doc("Base container"),
        ] = None,
    ) -> "PythonSdkDev":
        """Create an instance to develop the Python SDK."""
        if container is None:
            container = (
                dag.wolfi()
                .container(packages=["libgcc"])
                .with_env_variable("PYTHONUNBUFFERED", "1")
                .with_env_variable(
                    "PATH",
                    "/root/.local/bin:/usr/local/bin:$PATH",
                    expand=True,
                )
                .with_(cls.tools_cache("uv", "ruff", "mypy"))
                .with_(cls.uv)
            )
        return cls(
            container=(
                container.with_directory("/src/sdk/python", source)
                .with_workdir("/src/sdk/python")
                .with_exec(["uv", "sync"])
            )
        )

    @classmethod
    def uv(cls, ctr: dagger.Container) -> dagger.Container:
        """Add the uv tool to the container."""
        return (
            ctr.with_directory(
                "/usr/local/bin",
                dag.container().from_(UV_IMAGE).rootfs(),
                include=["uv*"],
            )
            .with_env_variable("UV_LINK_MODE", "copy")
            .with_env_variable("UV_PROJECT_ENVIRONMENT", "/opt/venv")
        )

    @classmethod
    def tools_cache(cls, *args: str):
        """Set up the cache directory for multiple tools."""

        def _tools(ctr: dagger.Container) -> dagger.Container:
            for tool in args:
                ctr = ctr.with_mounted_cache(
                    f"/root/.cache/{tool}",
                    dag.cache_volume(f"modpythondev-{tool}"),
                ).with_env_variable(
                    f"{tool.upper()}_CACHE_DIR",
                    f"/root/.cache/{tool}",
                )
            return ctr

        return _tools

    @function
    def supported_versions(self) -> list[str]:
        """Supported Python versions."""
        return list(get_args(SUPPORTED_VERSIONS))

    @function
    def with_directory(
        self,
        source: Annotated[
            dagger.Directory,
            Doc("The directory to add"),
        ],
    ) -> Self:
        """Mount a directory on the base container."""
        self.container = self.container.with_directory("/src", source)
        return self

    @function
    def with_container(self, ctr: dagger.Container) -> Self:
        """Replace container."""
        self.container = ctr
        return self

    @function
    def generate(
        self,
        introspection_json: Annotated[
            dagger.File,
            Doc("Result of the introspection query"),
        ],
    ) -> dagger.Changeset:
        """Generate the client bindings for the core API."""
        path = "/src/sdk/python/src/dagger/client/gen.py"
        src = self.container.directory(".")
        return (
            self.container.with_mounted_file("/schema.json", introspection_json)
            .with_exec(
                [
                    "uv",
                    "run",
                    "python",
                    "-m",
                    "codegen",
                    "generate",
                    "-i",
                    "/schema.json",
                    "-o",
                    path,
                ]
            )
            .changes(src)
        )
        # Ensure it's in a clean directory to avoid pulling in caches or
        # uv.lock file updates.
        return (
            dag.directory()
            .with_file(path, self.format(paths=(path,)).file(path))
            .changes(dag.directory())
        )

    @function
    async def typecheck(self) -> str:
        """Run the type checker (mypy)."""
        return await self.container.with_exec(["uv", "run", "mypy", "."]).stdout()

    @function
    async def lint(
        self,
        paths: Annotated[
            list[str] | None,
            Doc("List of files or directories to check"),
        ] = None,
    ) -> str:
        """Check for linting errors."""
        # TODO: Not defaulting to an empty list because of a bug in the Go SDK.
        # See https://github.com/dagger/dagger/pull/8106.
        if paths is None:
            paths = []
        return await (
            self.container.with_exec(["uv", "run", "ruff", "check", *paths])
            .with_exec(["uv", "run", "ruff", "format", "--check", "--diff", *paths])
            .stdout()
        )

    @function
    def format(
        self,
        paths: Annotated[
            tuple[str, ...],
            Doc("List of files or directories to check"),
        ] = (),
    ) -> dagger.Directory:
        """Format source files."""
        return (
            self.container.with_exec(
                ["uv", "run", "ruff", "check", "--fix-only", *paths]
            )
            .with_exec(["uv", "run", "ruff", "format", *paths])
            .directory("")
        )

    @function
    def test(
        self,
        version: Annotated[
            str | None,
            Doc("Python version to test against"),
        ] = None,
    ) -> TestSuite:
        """Run the test suite."""
        return TestSuite(container=self.container, version=version)

    @function
    def test_versions(self) -> list[TestSuite]:
        """Run the test suite for all supported versions."""
        return [self.test(version) for version in self.supported_versions()]

    @function
    def build(
        self,
        version: Annotated[
            str,
            Doc("The version for the distribution package"),
        ] = "0.0.0",
    ) -> dagger.Container:
        """Build the Python SDK client library package for distribution."""
        return (
            self.container.without_directory("dist")
            .with_exec(["uv", "version", version])
            .with_exec(["uv", "build"])
        )

    @function
    def publish(
        self,
        token: Annotated[
            dagger.Secret,
            Doc("The token for the upload"),
        ],
        version: Annotated[
            str,
            Doc("The version for the distribution package to publish"),
        ] = "0.0.0",
        url: Annotated[
            str,
            Doc("The URL of the upload endpoint (empty means PyPI)"),
        ] = "",
    ) -> dagger.Container:
        """Publish Python SDK client library to PyPI."""
        ctr = self.build(version).with_secret_variable("UV_PUBLISH_TOKEN", token)

        if url:
            ctr = ctr.with_env_variable("UV_PUBLISH_URL", url)

        return ctr.with_exec(["uv", "publish"])

    @function
    async def test_publish(
        self,
        token: Annotated[
            dagger.Secret,
            Doc("TestPyPI token"),
        ],
        version: Annotated[
            str,
            Doc("The version for the distribution package to publish"),
        ] = "0.0.0",
    ) -> dagger.Container:
        """Test the publishing of the Python SDK client library to TestPyPI."""
        return self.publish(token, version, url="https://test.pypi.org/legacy/")

    @function
    def docs(self) -> Docs:
        """Preview the reference documentation."""
        return Docs(container=self.container)
