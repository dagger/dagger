from typing import Annotated

import dagger
from dagger import Doc, function, object_type


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
        ref = src.docker_build().publish("ttl.sh/hello-dagger")  # build from Dockerfile
        return await ref
