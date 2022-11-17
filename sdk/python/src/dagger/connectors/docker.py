import logging
import os
import platform
import subprocess
import tempfile
from pathlib import Path

from attrs import Factory, define, field

from .base import Config, register_connector
from .bin import BinConnector, Engine, ProvisionError

logger = logging.getLogger(__name__)


ENGINE_SESSION_BINARY_PREFIX = "dagger-engine-session-"


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


class ImageRef:
    DIGEST_LEN = 16

    def __init__(self, ref: str) -> None:
        self.ref = ref

        # Check to see if ref contains @sha256:, if so use the digest as the id.
        if "@sha256:" not in ref:
            raise ValueError("Image ref must contain a digest")

        id_ = ref.split("@sha256:", maxsplit=1)[1]
        # TODO: add verification that the digest is valid
        # (not something malicious with / or ..)
        self.id = id_[: self.DIGEST_LEN]


@define
class EngineFromImage(Engine):
    cfg: Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start(self) -> None:
        cache_dir = (
            Path(os.environ.get("XDG_CACHE_HOME", "~/.cache")).expanduser() / "dagger"
        )
        cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)

        os_, arch = get_platform()

        image = ImageRef(self.cfg.host.hostname + self.cfg.host.path)
        engine_session_bin_path = (
            cache_dir / f"{ENGINE_SESSION_BINARY_PREFIX}{image.id}"
        )
        if os_ == "windows":
            engine_session_bin_path = engine_session_bin_path.with_suffix(".exe")

        if not engine_session_bin_path.exists():
            tempfile_args = {
                "prefix": f"temp-{ENGINE_SESSION_BINARY_PREFIX}",
                "dir": cache_dir,
                "delete": False,
            }
            with tempfile.NamedTemporaryFile(**tempfile_args) as tmp_bin:

                def cleanup():
                    """Remove the tmp_bin on error."""
                    tmp_bin.close()
                    os.unlink(tmp_bin.name)

                docker_run_args = [
                    "docker",
                    "run",
                    "--rm",
                    "--entrypoint",
                    "/bin/cat",
                    image.ref,
                    f"/usr/bin/{ENGINE_SESSION_BINARY_PREFIX}{os_}-{arch}",
                ]
                try:
                    subprocess.run(
                        docker_run_args,
                        stdout=tmp_bin,
                        stderr=subprocess.PIPE,
                        encoding="utf-8",
                        check=True,
                    )
                except FileNotFoundError as e:
                    raise ProvisionError(
                        f"Command '{docker_run_args[0]}' not found."
                    ) from e
                except subprocess.CalledProcessError as e:
                    raise ProvisionError(
                        f"Failed to copy engine session binary: {e.stderr}"
                    ) from e
                else:
                    # Flake8 Ignores
                    # F811 -- redefinition of (yet) unused function
                    # E731 -- assigning a lambda.
                    cleanup = lambda: None  # noqa: F811,E731
                finally:
                    cleanup()

                tmp_bin_path = Path(tmp_bin.name)
                tmp_bin_path.chmod(0o700)

                engine_session_bin_path = tmp_bin_path.rename(engine_session_bin_path)

            # garbage collection of old engine_session binaries
            for bin in cache_dir.glob(f"{ENGINE_SESSION_BINARY_PREFIX}*"):
                if bin != engine_session_bin_path:
                    bin.unlink()

        self._start([engine_session_bin_path])


@register_connector("docker-image")
@define
class DockerConnector(BinConnector):
    """Provision dagger engine from an image with docker"""

    engine: EngineFromImage = Factory(
        lambda self: EngineFromImage(self.cfg), takes_self=True
    )
