from datetime import datetime, timezone

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(self) -> str:
        """Build and publish image with oci annotations"""
        ref = (
            dag.container()
            .from_("alpine")
            .with_label("org.opencontainers.image.title", "my-alpine")
            .with_label("org.opencontainers.image.version", "1.0")
            .with_label(
                "org.opencontainers.image.created",
                datetime.now(timezone.utc).isoformat(),
            )
            .with_label(
                "org.opencontainers.image.source",
                "https://github.com/alpinelinux/docker-alpine",
            )
            .with_label("org.opencontainers.image.licenses", "MIT")
            .publish("ttl.sh/my-alpine")
        )
        return await ref
