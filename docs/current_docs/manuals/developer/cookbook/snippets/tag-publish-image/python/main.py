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
    ) -> list[str]:
        """Tag a container image multiple times and publish it to a private registry"""
        tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]
        addr = []
        container = (
            dag.container()
            .from_("nginx:1.23-alpine")
            .with_new_file(
                "/usr/share/nginx/html/index.html",
                contents="Hello from Dagger!",
                permissions=0o400,
            )
            .with_registry_auth(registry, username, password)
        )
        for tag in tags:
            a = await container.publish(f"{registry}/{username}/my-nginx:{tag}")
            addr.append(a)
        return addr
