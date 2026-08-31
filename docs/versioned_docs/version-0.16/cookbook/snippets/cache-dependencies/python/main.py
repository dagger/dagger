from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def build(
        self, source: Annotated[dagger.Directory, Doc("Source code location")]
    ) -> dagger.Container:
        """Build an application using cached dependencies"""
        return (
            dag.container()
            .from_("python:3.11")
            .with_directory("/src", source)
            .with_workdir("/src")
            .with_mounted_cache("/root/.cache/pip", dag.cache_volume("python-311"))
            .with_exec(["pip", "install", "-r", "requirements.txt"])
        )
