from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(
        self,
        source: Annotated[dagger.Directory, Doc("The source code to build")],
        secret: Annotated[dagger.Secret, Doc("The secret to use in the Dockerfile")],
    ) -> dagger.Container:
        """Build a Container from a Dockerfile"""
        # Ensure the Dagger secret's name matches what the Dockerfile
        # expects as the id for the secret mount.
        build_secret = dag.set_secret("gh-secret", await secret.plaintext())

        return source.docker_build(secrets=[build_secret])
