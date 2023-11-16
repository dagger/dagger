import dagger
from dagger.mod import field, function, object_type

from .consts import PYTHON_VERSION
from .utils import cache, mounted_workdir, python_base, requirements


@object_type
class Lint:
    """Lint the codebase."""

    requirements: dagger.File = field()
    src: dagger.Directory = field()
    pyproject: dagger.File | None = field()

    @function
    def base(self) -> dagger.Container:
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
        """Run the type checker."""
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
            .with_exec(["ruff", "check", "--no-cache", "."])
            .with_exec(["black", "--check", "."])
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
    def debug(self) -> dagger.Container:
        return self.base().with_(mounted_workdir(self.format_()))
