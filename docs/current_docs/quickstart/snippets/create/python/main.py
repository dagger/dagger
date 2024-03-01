import uuid

import dagger
from dagger import dag, function, object_type


@object_type
class Example:
    @function
    async def build_and_publish(
        self, build_src: dagger.Directory, build_args: list[str]
    ) -> str:
        """Build and publish a project using a Wolfi container"""
        # retrieve a new Wolfi container
        ctr = (
            dag
            .wolfi()
            .container()
        )

        # publish the Wolfi container with the build result
        return await (
            dag
            .golang()
            .build_container(source=build_src, args=build_args, base=ctr)
            .publish(f"ttl.sh/my-hello-container-{uuid.uuid4().hex[:8]}")
        )
