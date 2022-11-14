from pathlib import Path

import anyio
import pytest

import dagger

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


@pytest.mark.skipif(
    dagger.Config().host.scheme != "docker-image",
    reason="DAGGER_HOST is not docker-image",
)
async def test_docker_image_provision(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    cache_dir = tmp_path / "dagger"
    cache_dir.mkdir()
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path))

    # make some garbage for the image provisioner to collect
    garbage_path = cache_dir / "dagger-engine-session-gcme"
    garbage_path.touch()

    # clean up of existing containers is handled in engine-session binary
    # and is already tested in go, skipping coverage here

    # run a bunch of provisions concurrently
    async def connect_once():
        async with dagger.Connection() as client:
            assert await client.container().from_("alpine:3.16.2").id()

    concurrency = 30
    async with anyio.create_task_group() as tg:
        for _ in range(concurrency):
            tg.start_soon(connect_once)

    # assert that there's only one engine-session binary in the cache
    files = cache_dir.iterdir()
    bin = next(files)
    assert bin.name.startswith("dagger-engine-session-")
    with pytest.raises(StopIteration):
        next(files)
    # assert the garbage was cleaned up
    assert not garbage_path.exists()
