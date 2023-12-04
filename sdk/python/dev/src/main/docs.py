from typing import Annotated

import dagger
from dagger import Doc, field, function, object_type

from .utils import (
    from_host_dir,
    from_host_req,
    mounted_workdir,
    python_base,
    requirements,
    sdk,
)


@object_type
class Docs:
    """Manage the reference documentation (Sphinx)."""

    requirements: Annotated[
        dagger.File,
        Doc("The requirements.txt file with the documentation dependencies"),
    ] = field(default=lambda: from_host_req("docs"))

    src: Annotated[
        dagger.Directory,
        Doc("Directory with the Sphinx source files"),
    ] = field(default=lambda: from_host_dir("docs/"))

    @function
    def base(self) -> dagger.Container:
        """Base container for building the documentation."""
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
