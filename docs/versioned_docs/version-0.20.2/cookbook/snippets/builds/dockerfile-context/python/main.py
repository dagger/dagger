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
            Doc("location of source directory"),
        ],
        dockerfile: Annotated[
            dagger.File,
            Doc("location of Dockerfile"),
        ],
    ) -> str:
        """
        Build and publish image from Dockerfile

        This example uses a build context directory in a different location
        than the current working directory.
        """
        # get build context with dockerfile added
        workspace = (
            dag.container()
            .with_directory("/src", src)
            .with_workdir("/src")
            .with_file("/src/custom.Dockerfile", dockerfile)
            .directory("/src")
        )

        # build using Dockerfile and publish to registry
        ref = workspace.docker_build(dockerfile="custom.Dockerfile").publish(
            "ttl.sh/hello-dagger"
        )

        return await ref
