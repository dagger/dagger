import hashlib
import os
import pathlib
import shutil
import tarfile
import zipfile
from collections.abc import Awaitable, Callable
from contextlib import AsyncExitStack

import anyio
import anyio.from_thread
import anyio.to_thread
import httpx
import pytest
from aiohttp import web
from aiohttp.test_utils import TestServer
from anyio.streams.file import FileReadStream

import dagger
from dagger._engine import download
from dagger._managers import asyncify


@pytest.mark.anyio()
@pytest.fixture(autouse=True)
async def _setup(
    monkeypatch: pytest.MonkeyPatch,
    mock_cli_server: Callable[[str], Awaitable[str]],
):
    # ignore DAGGER_SESSION_PORT
    monkeypatch.delenv("DAGGER_SESSION_PORT", raising=False)

    # unset _EXPERIMENTAL_DAGGER_CLI_BIN otherwise it won't be downloaded
    if cli_bin := os.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"):
        monkeypatch.delenv("_EXPERIMENTAL_DAGGER_CLI_BIN", raising=False)

    # if explicitly requested to test against a certain URL, use that
    archive_url = os.getenv("_INTERNAL_DAGGER_TEST_CLI_URL")
    checksum_url = os.getenv("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL")

    if archive_url and checksum_url:
        monkeypatch.setattr(
            download.Downloader,
            "archive_url",
            httpx.URL(archive_url),
        )
        monkeypatch.setattr(
            download.Downloader,
            "checksum_url",
            httpx.URL(checksum_url),
        )

    # if _EXPERIMENTAL_DAGGER_CLI_BIN is set, create a temporary http server for it
    elif cli_bin:
        base_url = await mock_cli_server(cli_bin)
        monkeypatch.setattr(download.Downloader, "CLI_BASE_URL", base_url)


@pytest.mark.anyio()
@pytest.fixture()
async def mock_cli_server(tmp_path_factory: pytest.TempPathFactory):
    async with AsyncExitStack() as stack:

        async def go(cli_bin: str):
            downloader = download.Downloader()

            root_dir = tmp_path_factory.mktemp("resources")
            cli_path = pathlib.Path(cli_bin)
            archive_path = root_dir.joinpath(downloader.archive_url.path.lstrip("/"))
            checksum_path = root_dir.joinpath(downloader.checksum_url.path.lstrip("/"))

            await asyncify(create_archive, cli_path, archive_path)

            checksum = hashlib.sha256()

            async with await FileReadStream.from_path(archive_path) as stream:
                async for chunk in stream:
                    checksum.update(chunk)

            await anyio.Path(checksum_path).write_text(
                f"{checksum.hexdigest()} {downloader.archive_name}"
            )

            app = web.Application()
            app.add_routes([web.static("/", root_dir, show_index=True)])
            server = await stack.enter_async_context(TestServer(app))

            return str(server.make_url("/"))

        yield go


def create_archive(cli_path: pathlib.Path, archive_path: pathlib.Path):
    archive_path.parent.mkdir(parents=True, exist_ok=True)

    with cli_path.open("rb") as f:
        # .zip is used in Windows.
        if archive_path.name.endswith(".zip"):
            with (
                zipfile.ZipFile(archive_path, mode="w") as zar,
                zar.open("dagger.exe", mode="w") as zf,
            ):
                shutil.copyfileobj(f, zf)
        else:
            with tarfile.open(archive_path, mode="w:gz") as tar:
                tarinfo = tar.gettarinfo(arcname="dagger", fileobj=f)
                tar.addfile(tarinfo, f)


@pytest.mark.anyio()
@pytest.fixture()
async def cache_dir(
    tmp_path_factory: pytest.TempPathFactory,
    monkeypatch: pytest.MonkeyPatch,
) -> anyio.Path:
    """Create a temp cache_dir for testing & set XDG_CACHE_HOME."""
    tmp_path = anyio.Path(tmp_path_factory.mktemp("cache_home"))
    cache_dir = tmp_path / "dagger"
    await cache_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.setenv("XDG_CACHE_HOME", str(cache_dir.parent))
    return cache_dir


@pytest.mark.anyio()
@pytest.mark.slow()
@pytest.mark.provision()
async def test_download_bin(cache_dir: anyio.Path):
    # make some garbage for the image provisioner to collect
    garbage_path = cache_dir / "dagger-0.0.0"
    await garbage_path.touch()

    # clean up of existing containers is handled in dagger binary
    # and is already tested in go, skipping coverage here

    # run a bunch of provisions concurrently
    start = anyio.Event()

    async def connect_once():
        await start.wait()
        # NB: Don't use global connection here since we want to test
        # multiple concurrent connections.
        async with dagger.Connection(dagger.Config(retry=None)) as client:
            assert await client.default_platform()

    async with anyio.create_task_group() as tg:
        for _ in range(os.cpu_count() or 4):
            tg.start_soon(connect_once)
        start.set()

    # assert that there's only one dagger binary in the cache
    i = 0
    async for _ in cache_dir.glob("dagger-*"):
        assert not i, "cache not garbage collected"
        i += 1

    # assert the garbage was cleaned up
    assert not await garbage_path.exists()
