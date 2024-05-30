from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(
        self,
        src: Annotated[
            dagger.Directory,
            Doc("location of directory containing Dockerfile"),
        ],
    ) -> str:
        """Build and publish image from existing Dockerfile"""
        ref = (
            dag.container()
            .with_directory("/src", src)
            .with_workdir("/src")
            .directory("/src")
            .docker_build()
            .publish("ttl.sh/hello-dagger")
        )
        return await ref
