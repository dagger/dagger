# PROTOTYPE
# ---------
# This is just trying to find what's the minimum that's needed for
# the container that's used in `sdk/go/modules/sdks.go`. This container
# includes the runtime module, with the `ModuleRuntime` function that's
# used to create the actual a runtime container for any module.

import anyio
import rich

import dagger

SUPPORTED_PLATFORMS = [
    "linux/amd64",
    "linux/arm64",
]


async def main():
    src = (
        dagger.host()
        .directory(
            ".",
            include=[
                "./pyproject.toml",
                "./src/**/*.py",
                "./src/**/py.typed",
                "./runtime",
            ],
        )
    )

    # Current approach here was to see if it was enough to have the
    # `sdk/python/runtime` module in the container, but it turns out that
    # it's assumed this container is pre-compiled. Meaning we need the
    # entrypoint here to be the same as the one in the runtime container
    # for this module.

    ctr = (
        dagger.container()
        .from_("golang:1.21-alpine")
        .with_mounted_cache("/go/pkg/mod", dagger.cache_volume("modgomodcache"))
        .with_mounted_cache("/root/.cache/go-build", dagger.cache_volume("modgobuildcache"))
        .with_directory("/sdk", src)
        .with_workdir("/sdk")
        .with_label("io.dagger.module.config", "/sdk/runtime")
    )

    async with dagger.connection():
        """
        cid = await ctr.id()

        variants = [
            await dagger.container(id=cid, platform=dagger.Platform(platform))
            for platform in SUPPORTED_PLATFORMS
        ]

        addr = await dagger.container().publish(
            "helder/dagger-sdk-python",
            platform_variants=variants,
        )
        """
        addr = await ctr.publish("helder/dagger-sdk-python")

    rich.print(f"Published at {addr}")



anyio.run(main)
