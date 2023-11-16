import dagger
from dagger.mod import field, function, object_type

from .consts import PYTHON_VERSION
from .utils import mounted_workdir, python_base, requirements


@object_type
class Test:
    """Run the test suite."""

    requirements: dagger.File = field()
    src: dagger.Directory = field()
    version: str = field(default=PYTHON_VERSION)

    @function
    def base(self) -> dagger.Container:
        """Base container for running tests."""
        return (
            python_base(self.version)
            .with_(requirements(self.requirements))
            .with_(mounted_workdir(self.src))
            .with_exec(["pip", "install", "-e", "."])
        )

    @function
    def pytest(self, args: list[str]) -> dagger.Container:
        """Run unit tests."""
        return (
            self.base()
            .pipeline(f"Python {self.version}")
            .with_focus()
            .with_exec(
                ["pytest", *args],
                experimental_privileged_nesting=True,
            )
        )

    @function
    def unit(self) -> dagger.Container:
        """Run unit tests."""
        return self.pytest(["-m", "not slow and not provision"])

    @function
    def default(self) -> dagger.Container:
        """Run integration tests."""
        return self.pytest(["-W", "default", "-m", "not provision"])
