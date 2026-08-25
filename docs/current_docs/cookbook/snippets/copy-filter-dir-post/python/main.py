from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def copy_directory_with_exclusions(
        self,
        source: Annotated[dagger.Directory, Doc("Source directory")],
        exclude: Annotated[list[str], Doc("Exclusion pattern")] | None,
    ) -> dagger.Container:
        """Return a container with a filtered directory"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", source, exclude=exclude)
        )
