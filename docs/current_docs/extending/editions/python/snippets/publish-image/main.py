from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def publish(
        self,
        registry: Annotated[str, Doc("Registry address")],
        username: Annotated[str, Doc("Registry username")],
        password: Annotated[dagger.Secret, Doc("Registry password")],
    ) -> str:
        """Publish a container image to a private registry"""
        return await (
            dag.container()
            .from_("nginx:1.23-alpine")
            .with_new_file(
                "/usr/share/nginx/html/index.html",
                "Hello from Dagger!",
                permissions=0o400,
            )
            .with_registry_auth(registry, username, password)
            .publish(f"{registry}/{username}/my-nginx")
        )
