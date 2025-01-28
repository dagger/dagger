from typing import Annotated

import dagger
from dagger import Doc, function, object_type

from .utils import mounted_workdir


@object_type
class Docs:
    """Manage the reference documentation (Sphinx)."""

    container: dagger.Container

    @function
    def build(self) -> dagger.Directory:
        """Build the documentation."""
        return (
            self.container.with_workdir("docs")
            .with_exec(["uv", "run", "sphinx-build", "-v", ".", "/dist"])
            .directory("/dist")
        )

    @function
    def preview(
        self,
        bind: Annotated[
            int,
            Doc("The port to bind the web preview for the built docs"),
        ] = 8000,
    ) -> dagger.Service:
        """Build and preview the documentation in the browser."""
        return (
            self.container.with_(mounted_workdir(self.build()))
            .with_exec(["uv", "run", "python", "-m", "http.server", str(bind)])
            .with_exposed_port(bind)
            .as_service()
        )
