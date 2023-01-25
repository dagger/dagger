import contextlib
import functools
import hashlib
import io
import logging
import os
import platform
import shutil
import tarfile
import tempfile
import typing
import zipfile
from pathlib import Path, PurePath
from typing import IO, Iterator

import httpx
import platformdirs

from dagger.context import SyncResourceManager

from ._version import CLI_VERSION
from .conn import ProvisionError

DEFAULT_CLI_HOST = "dl.dagger.io"

logger = logging.getLogger(__name__)


class Platform(typing.NamedTuple):
    os: str
    arch: str


def get_platform() -> Platform:
    normalized_arch = {
        "x86_64": "amd64",
        "aarch64": "arm64",
    }
    uname = platform.uname()
    os_name = uname.system.lower()
    arch = uname.machine.lower()
    arch = normalized_arch.get(arch, arch)
    return Platform(os_name, arch)


class TempFile(SyncResourceManager):
    """Create a temporary file that only deletes on error."""

    def __init__(self, prefix: str, directory: Path):
        super().__init__()
        self.prefix = prefix
        self.dir = directory

    def __enter__(self) -> typing.IO[bytes]:
        with self.get_sync_stack() as stack:
            self.file = stack.enter_context(
                tempfile.NamedTemporaryFile(
                    mode="a+b",
                    prefix=self.prefix,
                    dir=self.dir,
                    delete=False,
                ),
            )
        return self.file

    def __exit__(self, exc, value, tb) -> None:
        super().__exit__(exc, value, tb)
        # delete on error
        if exc:
            Path(self.file.name).unlink()


class StreamReader(IO[bytes]):
    """File-like object from an httpx.Response."""

    def __init__(self, response: httpx.Response, bufsize: int = tarfile.RECORDSIZE):
        self.bufsize = bufsize
        self.stream = response.iter_raw(bufsize)
        self.hasher = hashlib.sha256()

    def read(self, size: int):
        """Read chunk from stream."""
        # To satisfy the file-like api we should be able to read an arbitrary
        # number of bytes from the stream, but the http response returns a
        # generator with a fixed chunk size. No need to go lower level to change it
        # since we know `read` will be called with the same size during extraction.
        assert size == self.bufsize
        try:
            chunk = next(self.stream)
        except StopIteration:
            return None
        self.hasher.update(chunk)
        return chunk

    def readall(self):
        """Read everything in stream while discarding chunks."""
        while self.read(self.bufsize):
            ...

    def getbuffer(self):
        """Read the entire stream into an in-memory buffer."""
        buf = io.BytesIO()
        shutil.copyfileobj(self, buf, self.bufsize)
        buf.seek(0)
        return buf

    @property
    def checksum(self) -> str:
        return self.hasher.hexdigest()


class Downloader:
    """Download the dagger CLI binary."""

    CLI_BIN_PREFIX = "dagger-"
    CLI_BASE_URL = f"https://{DEFAULT_CLI_HOST}/dagger/releases"

    def __init__(self, version: str = CLI_VERSION) -> None:
        self.version = version
        self.platform = get_platform()

    @property
    def archive_url(self) -> str:
        ext = "zip" if self.platform.os == "windows" else "tar.gz"
        return (
            f"{self.CLI_BASE_URL}/{self.version}/"
            f"dagger_v{self.version}_{self.platform.os}_{self.platform.arch}.{ext}"
        )

    @property
    def checksum_url(self):
        return f"{self.CLI_BASE_URL}/{self.version}/checksums.txt"

    @property
    def archive_name(self):
        return PurePath(httpx.URL(self.archive_url).path).name

    @functools.cached_property
    def cache_dir(self) -> Path:
        # Use the XDG_CACHE_HOME environment variable in all platforms to follow
        # https://github.com/adrg/xdg a bit more closely (used in the Go SDK).
        # See https://github.com/dagger/dagger/issues/3963
        env = os.environ.get("XDG_CACHE_HOME", "").strip()
        path = Path(env).expanduser() if env else platformdirs.user_cache_path()
        cache_dir = path / "dagger"
        cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        return cache_dir

    def get(self) -> str:
        """Download CLI to cache and return its path."""
        cli_bin_path = self.cache_dir / f"{self.CLI_BIN_PREFIX}{self.version}"

        if self.platform.os == "windows":
            cli_bin_path = cli_bin_path.with_suffix(".exe")

        if not cli_bin_path.exists():
            try:
                cli_bin_path = self._download(cli_bin_path)
            # FIXME: create sub-exception and raise where it makes
            # sense instead of doing a catch-all.
            except Exception as e:  # noqa: BLE001
                msg = "Failed to download CLI from archive"
                raise ProvisionError(msg) from e

        # garbage collection of old binaries
        for file in self.cache_dir.glob(f"{self.CLI_BIN_PREFIX}*"):
            if file != cli_bin_path:
                file.unlink()

        return str(cli_bin_path.absolute())

    def _download(self, path: Path) -> Path:
        try:
            expected_hash = self.expected_checksum()
        except httpx.HTTPError:
            logger.error("Failed to download checksums")
            raise

        with TempFile(f"temp-{self.CLI_BIN_PREFIX}", self.cache_dir) as tmp_bin:
            try:
                actual_hash = self.extract_cli_archive(tmp_bin)
            except httpx.HTTPError:
                logger.error("Failed to download CLI archive")
                raise

            if actual_hash != expected_hash:
                msg = (
                    f"Downloaded CLI binary checksum ({actual_hash}) "
                    f"does not match expected checksum ({expected_hash})"
                )
                raise ValueError(msg)

        tmp_bin_path = Path(tmp_bin.name)
        tmp_bin_path.chmod(0o700)
        return tmp_bin_path.rename(path)

    def expected_checksum(self) -> str:
        archive_name = self.archive_name
        with httpx.stream("GET", self.checksum_url, follow_redirects=True) as r:
            r.raise_for_status()
            for line in r.iter_lines():
                checksum, filename = line.split()
                if filename == archive_name:
                    return checksum
        msg = "Could not find checksum for CLI archive"
        raise KeyError(msg)

    def extract_cli_archive(self, dest: IO[bytes]) -> str:
        """
        Download the CLI archive and extract the binary into the provided dest.

        Returns
        -------
        str
            The sha256 hash of the whole archive as read during download.
        """
        url = self.archive_url

        with httpx.stream("GET", url, follow_redirects=True) as r:
            r.raise_for_status()
            reader = StreamReader(r)
            extractor = (
                self._extract_from_zip
                if url.endswith(".zip")
                else self._extract_from_tar
            )

            with extractor(reader) as cli_bin:
                shutil.copyfileobj(cli_bin, dest)

            return reader.checksum

    @contextlib.contextmanager
    def _extract_from_tar(self, reader: StreamReader) -> Iterator[IO[bytes]]:
        with tarfile.open(mode="|gz", fileobj=reader) as tar:
            for member in tar:
                if member.name == "dagger" and (file := tar.extractfile(member)):
                    yield file
                    # ensure the entire body is read into the hash
                    reader.readall()
                    break
            else:
                msg = "There is no item named 'dagger' in the archive"
                raise FileNotFoundError(msg)

    @contextlib.contextmanager
    def _extract_from_zip(self, reader: StreamReader) -> Iterator[IO[bytes]]:
        # FIXME: extract from stream instead of loading archive into memory
        with zipfile.ZipFile(reader.getbuffer()) as zar:
            try:
                with zar.open("dagger.exe") as file:
                    yield file
            except KeyError as e:
                raise FileNotFoundError from e
