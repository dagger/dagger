import hashlib
import io
import json
import logging
import os
import platform
import subprocess
import tarfile
import tempfile
import time
import urllib.request
from pathlib import Path
from typing import IO

import anyio
from attrs import define, field

import dagger

from .base import Engine as BaseEngine
from .base import ProvisionError, register_engine

logger = logging.getLogger(__name__)


DAGGER_CLI_BIN_PREFIX = "dagger-"
CLI_HOST = "dl.dagger.io"
CLI_SCHEME = "https"


@register_engine("bin")
@define
class Engine(BaseEngine):
    cfg: dagger.Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start_sync(self) -> None:
        self._start(
            [
                f"{self.cfg.host.netloc}{self.cfg.host.path}" or "dagger",
                "session",
            ]
        )

    def _start(self, base_args: list[str]) -> None:
        env = os.environ.copy()
        if self.cfg.workdir:
            base_args.extend(["--workdir", str(Path(self.cfg.workdir).absolute())])
        if self.cfg.config_path:
            base_args.extend(["--project", str(Path(self.cfg.config_path).absolute())])

        # Retry starting if "text file busy" error is hit. That error can happen
        # due to a flaw in how Linux works: if any fork of this process happens
        # while the temp binary file is open for writing, a child process can
        # still have it open for writing before it calls exec.
        # See this golang issue (which itself links to bug reports in other
        # langs and the kernel): https://github.com/golang/go/issues/22315
        # Unfortunately, this sort of retry loop is the best workaround. The
        # case is obscure enough that it should not be hit very often at all.
        for _ in range(10):
            try:
                self._proc = subprocess.Popen(
                    base_args,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=self.cfg.log_output or subprocess.PIPE,
                    encoding="utf-8",
                    env=env,
                )
            except FileNotFoundError as e:
                raise ProvisionError(f"Could not find {e.filename} executable") from e
            except OSError as e:
                # 26 is ETXTBSY
                if e.errno == 26:
                    time.sleep(0.1)
                else:
                    raise ProvisionError(f"Failed to start engine session: {e}") from e
            except Exception as e:
                raise ProvisionError(f"Failed to start engine session: {e}") from e
            else:
                break
        else:
            raise ProvisionError("Failed to start engine session after retries.")

        try:
            # read connect params from first line of stdout
            connect_params = json.loads(self._proc.stdout.readline())
        except ValueError as e:
            # Check if the subprocess exited with an error.
            if not self._proc.poll():
                raise e

            # FIXME: Duplicate writes into a buffer until end of provisioning
            # instead of reading directly from what the user may set in `log_output`
            if self._proc.stderr is not None and self._proc.stderr.readable():
                raise ProvisionError(
                    f"Dagger engine failed to start: {self._proc.stderr.readline()}"
                ) from e

            raise ProvisionError(
                "Dagger engine failed to start, is docker running?"
            ) from e

        self.cfg.host = f"http://{connect_params['host']}"
        self.cfg.session_token = connect_params["session_token"]

    def stop_sync(self, exc_type) -> None:
        if self._proc:
            self._proc.__exit__(exc_type, None, None)
            self._proc = None

    async def start(self) -> None:
        # FIXME: Create proper async provisioning later.
        # This is just to support sync faster.
        await anyio.to_thread.run_sync(self.start_sync)

    async def stop(self, exc_type) -> None:
        await anyio.to_thread.run_sync(self.stop_sync, exc_type)


@register_engine("download-bin")
@define
class DownloadBinEngine(Engine):
    cfg: dagger.Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start_sync(self) -> None:
        cache_dir = (
            Path(os.environ.get("XDG_CACHE_HOME", "~/.cache")).expanduser() / "dagger"
        )
        cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)

        os_, arch = get_platform()

        cli_version = self.cfg.host.netloc + self.cfg.host.path

        dagger_cli_bin_path = cache_dir / f"{DAGGER_CLI_BIN_PREFIX}{cli_version}"
        if os_ == "windows":
            dagger_cli_bin_path = dagger_cli_bin_path.with_suffix(".exe")

        if not dagger_cli_bin_path.exists():
            expected_hash = expected_checksum(cli_version)
            tempfile_args = {
                "prefix": f"temp-{DAGGER_CLI_BIN_PREFIX}",
                "dir": cache_dir,
                "delete": False,
            }
            with tempfile.NamedTemporaryFile(**tempfile_args) as tmp_bin:

                def cleanup():
                    """Remove the tmp_bin on error."""
                    tmp_bin.close()
                    os.unlink(tmp_bin.name)

                try:
                    actual_hash = extract_cli_archive(
                        cli_version,
                        tmp_bin,
                    )
                    if actual_hash != expected_hash:
                        raise ProvisionError(
                            f"Downloaded CLI binary checksum ({actual_hash})"
                            f"does not match expected checksum ({expected_hash})"
                        )
                    # Flake8 Ignores
                    # F811 -- redefinition of (yet) unused function
                    # E731 -- assigning a lambda.
                    cleanup = lambda: None  # noqa: F811,E731
                finally:
                    cleanup()

                tmp_bin.close()
                tmp_bin_path = Path(tmp_bin.name)
                tmp_bin_path.chmod(0o700)

                dagger_cli_bin_path = tmp_bin_path.rename(dagger_cli_bin_path)

        # garbage collection of old engine_session binaries
        for bin in cache_dir.glob(f"{DAGGER_CLI_BIN_PREFIX}*"):
            if bin != dagger_cli_bin_path:
                bin.unlink()

        self._start(
            [dagger_cli_bin_path, "session"],
        )


def get_platform() -> tuple[str, str]:
    normalized_arch = {
        "x86_64": "amd64",
        "aarch64": "arm64",
    }
    uname = platform.uname()
    os_name = uname.system.lower()
    arch = uname.machine.lower()
    arch = normalized_arch.get(arch, arch)
    return os_name, arch


def cli_archive_name(cli_version: str):
    os_, arch = get_platform()
    return f"dagger_v{cli_version}_{os_}_{arch}.tar.gz"


def cli_archive_url(cli_version: str):
    return (
        f"{CLI_SCHEME}://{CLI_HOST}/dagger/releases/{cli_version}"
        f"/{cli_archive_name(cli_version)}"
    )


def cli_checksum_url(cli_version: str):
    return f"{CLI_SCHEME}://{CLI_HOST}/dagger/releases/{cli_version}/checksums.txt"


# returns a dict of CLI archive name -> checksum for that archive
def checksum_dict(cli_version: str):
    # TODO: timeouts
    resp = urllib.request.urlopen(cli_checksum_url(cli_version))
    if resp.status != 200:
        raise ProvisionError(f"Failed to download checksums: {resp.status}")
    checksumContents = resp.read().decode("utf-8")
    checksums = {}
    for line in checksumContents.splitlines():
        checksum, filename = line.split()
        checksums[filename] = checksum
    return checksums


def expected_checksum(cli_version: str):
    expected = checksum_dict(cli_version)[cli_archive_name(cli_version)]
    if not expected:
        raise ProvisionError("Could not find checksum for CLI archive")
    return expected


# Download the CLI archive and extract the CLI from it into the provided dest.
# Returns the sha256 hash of the whole archive as read during download.
def extract_cli_archive(cli_version: str, dest: IO[bytes]):
    resp = urllib.request.urlopen(cli_archive_url(cli_version))
    if resp.status != 200:
        raise ProvisionError(f"Failed to download CLI archive: {resp.status}")
    response_body = (
        resp.read()
    )  # TODO: loading the whole thing in memory... should stream
    checksum = hashlib.sha256(response_body).hexdigest()
    # open the tar.gz archive using the gz mode to uncompress it
    with tarfile.open(fileobj=io.BytesIO(response_body), mode="r:gz") as tar:
        # extract the CLI binary from the archive
        cli_bin = tar.extractfile("dagger")
        if not cli_bin:
            raise ProvisionError("Could not find CLI binary in archive")
        # write the CLI binary to the dest
        dest.write(cli_bin.read())
    return checksum
