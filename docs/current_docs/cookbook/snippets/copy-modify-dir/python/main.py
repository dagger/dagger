from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def copy_and_modify_directory(
        self, source: Annotated[dagger.Directory, Doc("Source directory")]
    ) -> dagger.Container:
        """Return a container with a specified directory and an additional file"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", source)
            .with_exec(["/bin/sh", "-c", "`echo foo > /src/foo`"])
        )
