import hashlib
import logging
import os
import subprocess
import tempfile
from pathlib import Path

import anyio
from attrs import Factory, define, field

from dagger import Client

from .base import Config, register_connector
from .http import HTTPConnector

logger = logging.getLogger(__name__)


# TODO: this should be a config option obviously
DEFAULT_IMAGE_REF = "docker.io/eriksipsma/test-dagger:rebase@sha256:b6bcf40346a70a834a0c1461046551993b17c92288c32121b171a825958e7901"  # noqa

HELPER_BINARY_PREFIX = "dagger-sdk-helper-"


def get_platform() -> tuple[str, str]:
    normalized_arch = {
        "x86_64": "amd64",
        "aarch64": "arm64",
    }
    uname = os.uname()
    os_ = uname.sysname.lower()
    arch = uname.machine.lower()
    arch = normalized_arch.get(arch, arch)
    return os_, arch


class ImageRef:
    DIGEST_LEN = 16

    def __init__(self, ref: str = DEFAULT_IMAGE_REF) -> None:
        self.ref = ref
        self.is_pinned = False

        # Check to see if ref contains @sha256:, if so use the digest as the id.
        if "@sha256:" in ref:
            id = ref.split("@sha256:", maxsplit=1)[1]
            # TODO: add verification that the digest is valid
            # (not something malicious with / or ..)
            self.is_pinned = True
        else:
            # set id to the sha256 hash of the image_ref
            # TODO: ensure that this is consistent w/ Go's sha256 hash
            # (encoding is only likely source of difference)
            id = hashlib.sha256(ref.encode()).hexdigest()

        self.id = id[: self.DIGEST_LEN]


@define
class Engine:
    cfg: Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start(self) -> None:
        cache_dir = (
            Path(os.environ.get("XDG_CACHE_HOME", "~/.cache")).expanduser() / "dagger"
        )
        cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)

        image = ImageRef(self.cfg.host.hostname + self.cfg.host.path)
        helper_bin_path = cache_dir / f"{HELPER_BINARY_PREFIX}{image.id}"
        container_name = f"dagger-engine-{image.id}"

        docker_run_args = [
            "docker",
            "run",
            "--name",
            container_name,
            "-d",
            "--restart",
            "always",
            "--privileged",
            image.ref,
            "--debug",
        ]

        try:
            subprocess.run(
                docker_run_args,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                encoding="utf-8",
                check=True,
            )
        except subprocess.CalledProcessError as e:
            if (
                f'Conflict. The container name "/{container_name}"'
                " is already in use by container"
            ) not in e.stdout:
                raise ProvisionError(f"Failed to start engine container: {e.stdout}")

        # TODO: garbage collection of old containers

        os_, arch = get_platform()

        if not helper_bin_path.exists():
            tmp_bin = tempfile.NamedTemporaryFile(
                prefix=f"temp-{HELPER_BINARY_PREFIX}",
                dir=cache_dir,
                delete=False,
            )
            docker_cp_args = [
                "docker",
                "cp",
                f"{container_name}:/usr/bin/{HELPER_BINARY_PREFIX}{os_}-{arch}",
                tmp_bin.name,
            ]
            try:
                subprocess.run(
                    docker_cp_args,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    encoding="utf-8",
                    check=True,
                )
            except subprocess.CalledProcessError as e:
                tmp_bin.close()
                os.unlink(tmp_bin.name)
                raise ProvisionError(f"Failed to copy helper binary: {e.stdout}")

            tmp_bin_path = Path(tmp_bin.name)
            tmp_bin_path.chmod(0o700)

            helper_bin_path = (
                tmp_bin_path.rename(helper_bin_path)
                if image.is_pinned
                else tmp_bin_path
            )

        # garbage collection of old helper binaries
        for bin in cache_dir.glob(f"{HELPER_BINARY_PREFIX}*"):
            if bin != helper_bin_path:
                bin.unlink()

        buildkit_host = f"docker-container://{container_name}"

        helper_args = [helper_bin_path, "--remote", buildkit_host]
        if self.cfg.workdir:
            helper_args.extend(["--workdir", str(Path(self.cfg.workdir).absolute())])
        if self.cfg.config_path:
            helper_args.extend(
                ["--project", str(Path(self.cfg.config_path).absolute())]
            )

        self._proc = subprocess.Popen(
            helper_args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=self.cfg.log_output or subprocess.DEVNULL,
            encoding="utf-8",
        )

        # read port number from first line of stdout
        port = int(self._proc.stdout.readline())

        # TODO: verify port number is valid

        self.cfg.host = f"http://localhost:{port}"

    def is_running(self) -> bool:
        return self._proc is not None

    def stop(self, exc_type) -> None:
        if not self.is_running():
            return
        self._proc.__exit__(exc_type, None, None)

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, exc_type, *args, **kwargs):
        self.stop(exc_type)


@register_connector("docker-image")
@define
class DockerConnector(HTTPConnector):
    """Providion dagger engine from an image with docker"""

    engine: Engine = Factory(lambda self: Engine(self.cfg), takes_self=True)

    @property
    def query_url(self) -> str:
        return f"{self.cfg.host.geturl()}/query"

    async def connect(self) -> Client:
        # FIXME: Create proper async provisioning later.
        # This is just to support sync faster.
        await anyio.to_thread.run_sync(self.provision_sync)
        return await super().connect()

    async def close(self, exc_type) -> None:
        # FIXME: need exit stack?
        await super().close(exc_type)
        if self.engine.is_running():
            await anyio.to_thread.run_sync(self.engine.stop, exc_type)

    def connect_sync(self) -> Client:
        self.provision_sync()
        return super().connect_sync()

    def provision_sync(self) -> None:
        # FIXME: handle cancellation, retries and timeout
        # FIXME: handle errors during provisioning
        self.engine.start()

    def close_sync(self, exc_type) -> None:
        # FIXME: need exit stack?
        super().close_sync()
        self.engine.stop(exc_type)


class ProvisionError(Exception):
    """Error while provisioning the Dagger engine."""
