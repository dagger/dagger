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
            .with_mounted_cache("/root/.cache/poetry", dag.cache_volume("poetry_cache"))
            .with_exec(
                [
                    "pip",
                    "install",
                    "--user",
                    "poetry==1.5.1",
                    "poetry-dynamic-versioning==0.23.0",
                ]
            )
            .with_exec(["poetry", "install", "--no-root"])
            .with_exec(["poetry", "install", "--only-root"])
        )
