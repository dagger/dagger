import hashlib
import io
import os
import shutil
import tarfile
import zipfile
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import anyio
import anyio.from_thread
import httpx
import pytest

import dagger
from dagger._engine import download


@pytest.fixture(autouse=True)
def cache_dir(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Create a temp cache_dir for testing & set XDG_CACHE_HOME."""
    cache_dir = tmp_path / "dagger"
    cache_dir.mkdir()
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path))
    return cache_dir


@pytest.mark.anyio()
@pytest.fixture(autouse=True)
async def _temporary_cli_server(monkeypatch: pytest.MonkeyPatch):
    # ignore DAGGER_SESSION_PORT
    monkeypatch.delenv("DAGGER_SESSION_PORT", raising=False)

    # if explicitly requested to test against a certain URL, use that
    archive_url = os.environ.get("_INTERNAL_DAGGER_TEST_CLI_URL")
    checksum_url = os.environ.get("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL")

    if archive_url and checksum_url:
        monkeypatch.setattr(download.Downloader, "archive_url", archive_url)
        monkeypatch.setattr(download.Downloader, "checksum_url", checksum_url)
        yield
        return

    # if _EXPERIMENTAL_DAGGER_CLI_BIN is set, create a temporary http server for it
    if cli_bin := os.environ.get("_EXPERIMENTAL_DAGGER_CLI_BIN"):
        monkeypatch.delenv("_EXPERIMENTAL_DAGGER_CLI_BIN", raising=False)

        downloader = download.Downloader()
        archive_url = httpx.URL(downloader.archive_url)
        checksum_url = httpx.URL(downloader.checksum_url)

        # create an in-memory archive with the cli_bin in it
        archive = io.BytesIO()

        with Path(cli_bin).open("rb") as f:
            if downloader.archive_name.endswith(".zip"):
                with (
                    zipfile.ZipFile(archive, mode="w") as zar,
                    zar.open(
                        "dagger.exe",
                        mode="w",
                    ) as zf,
                ):
                    shutil.copyfileobj(f, zf)
            else:
                with tarfile.open(fileobj=archive, mode="w:gz") as tar:
                    tarinfo = tar.gettarinfo(arcname="dagger", fileobj=f)
                    tar.addfile(tarinfo, f)

        # get the sha256 checksum of the tar_bytes
        checksum = hashlib.sha256(archive.getvalue()).hexdigest()
        checksum_file_contents = f"{checksum}  {downloader.archive_name}"

        # define the files our in-memory http server will serve
        files = {
            archive_url.path: archive.getvalue(),
            checksum_url.path: checksum_file_contents.encode(),
        }

        class RequestHandler(BaseHTTPRequestHandler):
            def do_GET(self):  # noqa: N802
                response = files.get(self.path)
                if not response:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(response)))
                self.end_headers()
                self.wfile.write(response)

        # create a listener on a random localhost port and start the server
        with HTTPServer(("127.0.0.1", 0), RequestHandler) as httpd:
            host, port = httpd.socket.getsockname()
            kwargs = {
                "scheme": "http",
                "host": host,
                "port": int(port),
            }
            monkeypatch.setattr(
                download.Downloader,
                "archive_url",
                str(archive_url.copy_with(**kwargs)),
            )
            monkeypatch.setattr(
                download.Downloader,
                "checksum_url",
                str(checksum_url.copy_with(**kwargs)),
            )
            with anyio.from_thread.start_blocking_portal() as portal:
                server = portal.start_task_soon(httpd.serve_forever)
                yield
                httpd.shutdown()
                server.cancel()
                return

    yield


@pytest.mark.anyio()
@pytest.mark.slow()
@pytest.mark.provision()
async def test_download_bin(cache_dir: Path):
    # make some garbage for the image provisioner to collect
    garbage_path = cache_dir / "dagger-gcme"
    garbage_path.touch()

    # clean up of existing containers is handled in dagger binary
    # and is already tested in go, skipping coverage here

    # run a bunch of provisions concurrently
    async def connect_once():
        async with dagger.Connection() as client:
            assert await client.container().from_("alpine:3.16.2").id()

    async with anyio.create_task_group() as tg:
        for _ in range(os.cpu_count() or 4):
            tg.start_soon(connect_once)

    # assert that there's only one dagger binary in the cache
    files = cache_dir.iterdir()
    bins = [file for file in files if file.name.startswith("dagger-")]
    assert len(bins) == 1
    # assert the garbage was cleaned up
    assert not garbage_path.exists()
