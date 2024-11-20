from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def copy_directory_with_exclusions(
        self,
        source: Annotated[dagger.Directory, Doc("Source directory")],
        exclude_directory: Annotated[str, Doc("Directory exclusion pattern")] | None,
        exclude_file: Annotated[str, Doc("File exclusion pattern")] | None,
    ) -> dagger.Container:
        """Return a container with a filtered directory"""
        filtered_source = source
        if exclude_directory is not None:
            filtered_source = filtered_source.without_directory(exclude_directory)
        if exclude_file is not None:
            filtered_source = filtered_source.without_file(exclude_file)
        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", filtered_source)
        )
