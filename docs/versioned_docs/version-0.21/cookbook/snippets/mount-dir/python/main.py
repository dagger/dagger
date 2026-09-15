from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def mount_directory(
        self, source: Annotated[dagger.Directory, Doc("Source directory")]
    ) -> dagger.Container:
        """Return a container with a mounted directory"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_mounted_directory("/src", source)
        )
