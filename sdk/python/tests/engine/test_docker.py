import os
import subprocess
from pathlib import Path

import anyio
import pytest
from pytest_subprocess.fake_process import FakeProcess

import dagger
from dagger.config import DEFAULT_HOST
from dagger.engine import docker


@pytest.fixture(autouse=True)
def cache_dir(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Creates a temp cache_dir for testing & sets XDG_CACHE_HOME."""
    cache_dir = tmp_path / "dagger"
    cache_dir.mkdir()
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path))
    return cache_dir


@pytest.mark.skipif(
    dagger.Config().host.scheme != "docker-image",
    reason="DAGGER_HOST is not docker-image",
)
@pytest.mark.anyio
@pytest.mark.slow
@pytest.mark.provision
async def test_docker_image_provision(cache_dir: Path):
    # make some garbage for the image provisioner to collect
    garbage_path = cache_dir / "dagger-gcme"
    garbage_path.touch()

    # clean up of existing containers is handled in dagger binary
    # and is already tested in go, skipping coverage here

    # run a bunch of provisions concurrently
    async def connect_once():
        async with dagger.Connection() as client:
            assert await client.container().from_("alpine:3.16.2").id()

    concurrency = os.cpu_count()
    async with anyio.create_task_group() as tg:
        for _ in range(concurrency):
            tg.start_soon(connect_once)

    # assert that there's only one dagger binary in the cache
    files = cache_dir.iterdir()
    bin_ = next(files)
    assert bin_.name.startswith("dagger-")
    with pytest.raises(StopIteration):
        next(files)
    # assert the garbage was cleaned up
    assert not garbage_path.exists()


def test_docker_cli_is_not_installed(fp: FakeProcess):
    """
    When the docker cli is not installed ensure that the `FileNotFoundError` returned by
    `subprocess.run` is wrapped by a `docker.ProvisionError` stating that
    the command is not found.
    """

    def patched_subprocess_run(_):
        raise FileNotFoundError()

    fp.register(["docker", "run", fp.any()], callback=patched_subprocess_run)

    # force host to ignore DOCKER_HOST environment variable
    engine = docker.Engine(dagger.Config(host=DEFAULT_HOST))
    with pytest.raises(docker.ProvisionError) as exc_info:
        engine.start_sync()

    assert "Command 'docker' not found" in str(exc_info.value)


def test_tmp_files_are_removed_on_error(cache_dir: Path, fp: FakeProcess):
    """
    Ensure that the created temporary file is removed if copying from the
    docker-image fails.
    """

    fp.register(
        ["docker", "run", fp.any()],
        callback=lambda _: 1 / 0,  # Raises ZeroDivisionError
    )

    # force host to ignore DOCKER_HOST environment variable
    engine = docker.Engine(dagger.Config(host=DEFAULT_HOST))
    with pytest.raises(ZeroDivisionError):
        engine.start_sync()

    assert not any(cache_dir.iterdir())


def test_docker_engine_is_not_running(fp: FakeProcess):
    """
    When the docker image is not installed ensure that
    the `CalledProcessError` is wrapped in a `dagger.ProvisionError`
    """

    def patched_subprocess_run(_):
        raise subprocess.CalledProcessError(returncode=1, cmd="mocked")

    fp.register(["docker", "run", fp.any()], callback=patched_subprocess_run)

    # force host to ignore DOCKER_HOST environment variable
    engine = docker.Engine(dagger.Config(host=DEFAULT_HOST))
    with pytest.raises(docker.ProvisionError) as exc_info:
        engine.start_sync()

    assert "Failed to copy" in str(exc_info.value)
