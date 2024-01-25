from typing import Annotated

import dagger
from dagger import Doc, field, function, object_type

from .consts import PYTHON_VERSION
from .utils import (
    cache,
    from_host,
    from_host_req,
    mounted_workdir,
    python_base,
    requirements,
)


@object_type
class Lint:
    """Lint the codebase."""

    requirements: Annotated[
        dagger.File,
        Doc("The requirements.txt file with the linting dependencies"),
    ] = field(default=lambda: from_host_req("lint"))

    src: Annotated[
        dagger.Directory,
        Doc("Directory with the .py, .ruff and .toml files to lint"),
    ] = field(
        default=lambda: from_host(
            [
                "**/*.py",
                "**/*ruff.toml",
                "**/pyproject.toml",
                "**/.gitignore",
            ]
        )
    )

    pyproject: Annotated[
        dagger.File | None,
        Doc(
            "The pyproject.toml file with the linting configuration, "
            "if not in the source directory"
        ),
    ] = field(default=None)

    @function
    def base(self) -> dagger.Container:
        """The base container with the Python environment for linting."""
        ctr = (
            python_base()
            .with_(requirements(self.requirements))
            .with_(mounted_workdir(self.src))
        )
        if self.pyproject is not None:
            ctr = ctr.with_mounted_file("/pyproject.toml", self.pyproject)
        return ctr

    @function
    def typing(self) -> dagger.Container:
        """Run the type checker (mypy)."""
        return (
            self.base()
            .with_env_variable("MYPY_CACHE_DIR", "/root/.cache/mypy")
            .with_(
                cache(
                    "/root/.cache/mypy",
                    keys=["mypy", f"py{PYTHON_VERSION}", "slim"],
                )
            )
            .with_focus()
            .with_exec(["mypy", "."])
        )

    @function
    def check(self) -> dagger.Container:
        """Lint the codebase."""
        return (
            self.base()
            .with_focus()
            .with_exec(["ruff", "check", "--diff", "--no-cache", "."])
            .with_exec(["black", "--check", "--diff", "."])
        )

    @function(name="format")
    def format_(self, diff: bool = False) -> dagger.Directory:
        """Format and fix the code style."""
        extra = ["--diff"] if diff else []
        return self.src.diff(
            self.base()
            .with_focus()
            .with_exec(["ruff", "--fix-only", *extra, "--no-cache", "-e", "."])
            .with_exec(["black", *extra, "."])
            .directory(".")
        )

    @function
    def debug_format(self) -> dagger.Container:
        """Container with mounted formatted files for debugging."""
        return self.base().with_(mounted_workdir(self.format_()))
