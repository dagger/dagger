import asyncio
import os
from pathlib import Path
import tempfile

import pytest

import dagger

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


async def test_docker_image_provision():
    # skip test if DAGGER_HOST is set and not prefixed with docker-image
    dagger_host = os.environ.get("DAGGER_HOST")
    if dagger_host and not dagger_host.startswith("docker-image:"):
        pytest.skip("DAGGER_HOST is not docker-image")

    with tempfile.TemporaryDirectory() as tmpdir:
        cache_dir = Path(tmpdir) / "dagger"
        cache_dir.mkdir(parents=True, exist_ok=True)

        # make some garbage for the image provisioner to collect
        (cache_dir / "dagger-engine-session-gcme").touch()
        # clean up of existing containers is handled in engine-session binary
        # and is already tested in go, skipping coverage here

        # run a bunch of provisions in parallel
        async def connect_once():
            async with dagger.Connection() as client:
                assert await client.container().from_("alpine:3.16.2").id()
        parallelism = 30
        await asyncio.gather(*[connect_once() for _ in range(parallelism)])

        # assert that there's only one engine-session binary in the cache
        assert len(list(cache_dir.glob("*"))) == 1
        assert list(cache_dir.glob("*"))[0].name.startswith("dagger-engine-session-")
