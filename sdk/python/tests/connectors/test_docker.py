import shutil
from pathlib import Path

import anyio
import pytest

import dagger
from dagger.connectors import docker
from dagger.connectors.base import Config

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


@pytest.fixture
def cache_dir(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Creates a temp cache_dir for testing & sets XDG_CACHE_HOME."""
    cache_dir = tmp_path / "dagger"
    cache_dir.mkdir()
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path))
    yield cache_dir
    shutil.rmtree(str(cache_dir))


@pytest.mark.skipif(
    dagger.Config().host.scheme != "docker-image",
    reason="DAGGER_HOST is not docker-image",
)
async def test_docker_image_provision(cache_dir: Path, monkeypatch: pytest.MonkeyPatch):
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
    bin_ = next(files)
    assert bin_.name.startswith("dagger-engine-session-")
    with pytest.raises(StopIteration):
        next(files)
    # assert the garbage was cleaned up
    assert not garbage_path.exists()


def test_docker_cli_is_not_installed(cache_dir: Path, monkeypatch: pytest.MonkeyPatch):
    eng = docker.Engine(Config())

    def pached_subprocess_run(*args, **kwargs):
        raise FileNotFoundError()

    monkeypatch.setattr(docker.subprocess, "run", pached_subprocess_run)

    with pytest.raises(docker.ProvisionError):
        eng.start()
