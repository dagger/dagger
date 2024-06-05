from typing import Annotated

import dagger
from dagger import Doc, function, object_type


@object_type
class MyModule:
    @function
    async def build(
        self,
        source: Annotated[dagger.Directory, Doc("The source code to build")],
        secret: Annotated[dagger.Secret, Doc("The secret to use in the Dockerfile")],
    ) -> dagger.Container:
        """Build a Container from a Dockerfile"""
        secret_name = await secret.name()
        return source.docker_build(
            dockerfile="Dockerfile",
            build_args=dagger.DockerBuildArgs(name="gh-secret", value=secret_name),
            secrets=[secret],
        )
