import hashlib
import io
import os
import tarfile
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import anyio
import pytest

import dagger
from dagger.engine import bin


@pytest.fixture(autouse=True)
def cache_dir(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    """Creates a temp cache_dir for testing & sets XDG_CACHE_HOME."""
    cache_dir = tmp_path / "dagger"
    cache_dir.mkdir()
    monkeypatch.setenv("XDG_CACHE_HOME", str(tmp_path))
    return cache_dir


@pytest.mark.anyio
@pytest.fixture(autouse=True)
async def temporary_cli_server(monkeypatch: pytest.MonkeyPatch):
    # ignore DAGGER_SESSION_URL
    monkeypatch.delenv("DAGGER_SESSION_URL", raising=False)
    # if _EXPERIMENTAL_DAGGER_CLI_BIN is set, create a temporary http server for it
    cli_bin = os.environ.get("_EXPERIMENTAL_DAGGER_CLI_BIN")
    if cli_bin:
        monkeypatch.delenv("_EXPERIMENTAL_DAGGER_CLI_BIN", raising=False)
        # create an in memory tar.gz with the cli_bin in it
        tar_bytes = io.BytesIO()
        with tarfile.open(fileobj=tar_bytes, mode="w:gz") as tar:
            tarinfo = tarfile.TarInfo(name="dagger")
            tarinfo.mtime = int(time.time())
            with open(cli_bin, "rb") as f:
                tarinfo.size = f.seek(0, io.SEEK_END)
                f.seek(0)
                tar.addfile(tarinfo, f)

        os_, arch = bin.get_platform()

        archive_name = f"dagger_v{dagger.CLI_VERSION}_{os_}_{arch}.tar.gz"

        # get the sha256 checksum of the tar_bytes
        checksum = hashlib.sha256(tar_bytes.getvalue()).hexdigest()
        checksum_file_contents = f"{checksum}  {archive_name}"

        # define the files our in memory http server will serve
        base_path = f"dagger/releases/{dagger.CLI_VERSION}"
        path_to_file_bytes = {
            f"/{base_path}/{archive_name}": tar_bytes.getvalue(),
            f"/{base_path}/checksums.txt": checksum_file_contents.encode(),
        }

        class RequestHandler(BaseHTTPRequestHandler):
            def do_GET(self):
                response = path_to_file_bytes.get(self.path)
                if not response:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(response)))
                self.end_headers()
                self.wfile.write(response)

        # create a listener on a random localhost port and start the server
        httpd = HTTPServer(("127.0.0.1", 0), RequestHandler)
        address = httpd.socket.getsockname()
        monkeypatch.setattr(bin, "CLI_HOST", f"{address[0]}:{address[1]}")
        monkeypatch.setattr(bin, "CLI_SCHEME", "http")
        with anyio.start_blocking_portal() as portal:
            server = portal.start_task_soon(httpd.serve_forever)
            yield
            httpd.shutdown()
            server.cancel()


@pytest.mark.anyio
@pytest.mark.slow
@pytest.mark.provision
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
