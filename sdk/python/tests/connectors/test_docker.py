import shutil
import subprocess
import sys
from pathlib import Path
from typing import TextIO

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
    """
    When the docker cli is not installed ensure that the `FileNotFoundError` returned by
    `subprocess.run` is wrapped by a `docker.ProvisionError` stating that
    the command is not found.
    """
    eng = docker.Engine(Config())

    def pached_subprocess_run(*args, **kwargs):
        raise FileNotFoundError()

    monkeypatch.setattr(docker.subprocess, "run", pached_subprocess_run)

    with pytest.raises(docker.ProvisionError) as execinfo:
        eng.start()
    assert "Command 'docker' not found" in str(execinfo.value)


def test_tmp_files_are_removed_on_error(
    cache_dir: Path, monkeypatch: pytest.MonkeyPatch
):
    """
    Ensure that the created temporary file is removed if copying from the
    docker-image fails.
    """
    eng = docker.Engine(Config())

    monkeypatch.setattr(
        docker.subprocess,
        "run",
        lambda *args, **kwargs: 1 / 0,  # Raises ZeroDivisionError
    )

    try:
        eng.start()
    except ZeroDivisionError:
        pass

    assert not [x for x in cache_dir.iterdir()]


def test_docker_engine_is_not_running(cache_dir: Path, monkeypatch: pytest.MonkeyPatch):
    """
    When the docker image is not installed ensure that
    the `CalledProcessError` is wrapped in a `dagger.ProvisionError`
    """
    eng = docker.Engine(Config())

    def pached_subprocess_run(*args, **kwargs):
        raise subprocess.CalledProcessError(returncode=1, cmd="mocked")

    monkeypatch.setattr(docker.subprocess, "run", pached_subprocess_run)

    with pytest.raises(docker.ProvisionError):
        eng.start()


@pytest.mark.parametrize("log_output", [sys.stderr, subprocess.PIPE])
async def test_docker_engine_is_not_running_cached_dagger_engine_exists(
    log_output: TextIO, cache_dir: Path, monkeypatch: pytest.MonkeyPatch
):
    """
    When docker isn't running, but a cached version of the dagger engine exists,
    ensure that the `ValueError` is wrapped in a `ProvisionError`
    and a more helpful error message is printed.
    """
    # Create the cached 'dagger engine'
    cfg = Config(log_output=log_output)
    async with dagger.Connection():
        pass

    # Mock DOCKER_HOST to make it seem like docker isn't running.
    monkeypatch.setenv("DOCKER_HOST", "127.1.2.3")
    with pytest.raises(docker.ProvisionError) as execinfo:
        async with dagger.Connection(cfg):
            pass
    assert "Dagger engine failed to start" in str(execinfo.value)
