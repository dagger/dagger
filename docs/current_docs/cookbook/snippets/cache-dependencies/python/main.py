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
            # if using pip
            .with_mounted_cache("/root/.cache/pip", dag.cache_volume("pip_cache"))
            .with_exec(["pip", "install", "-r", "requirements.txt"])
            # if using poetry
            .with_mounted_cache(
                "/root/.cache/pypoetry", dag.cache_volume("poetry_cache")
            )
            .with_exec(
                [
                    "pip",
                    "install",
                    "--user",
                    "poetry==1.5.1",
                    "poetry-dynamic-versioning==0.23.0",
                ]
            )
            # No root first uses dependencies but not the project itself
            .with_exec(["poetry", "install", "--no-root", "--no-interaction"])
            .with_exec(["poetry", "install", "--no-interaction", "--only-root"])
        )
