import sys
from datetime import datetime, timezone

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # publish app on alpine base
        addr = await (
            client.container()
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

    print(addr)


anyio.run(main)
