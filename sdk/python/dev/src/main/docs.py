import dagger
from dagger.mod import field, function, object_type

from .utils import mounted_workdir, python_base, requirements, sdk


@object_type
class Docs:
    requirements: dagger.File = field()
    src: dagger.Directory = field()

    @function
    def base(self) -> dagger.Container:
        return (
            python_base()
            .with_(sdk)
            .with_(requirements(self.requirements))
            .with_(mounted_workdir(self.src))
        )

    @function
    def build(self) -> dagger.Directory:
        """Build the documentation."""
        return (
            self.base()
            .with_exec(["sphinx-build", "-v", ".", "/dist"])
            .directory("/dist")
        )

    @function
    def preview(self, bind: int = 8000) -> dagger.Service:
        """Build and preview the documentation in the browser."""
        return (
            python_base()
            .with_(mounted_workdir(self.build()))
            .with_exec(["python", "-m", "http.server", str(bind)])
            .with_exposed_port(bind)
            .as_service()
        )
