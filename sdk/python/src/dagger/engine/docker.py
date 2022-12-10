import logging
import os
import platform
import subprocess
import tempfile
from pathlib import Path

from attrs import define, field

from dagger import Config

from .base import ProvisionError, register_engine
from .bin import Engine as BinEngine

logger = logging.getLogger(__name__)


DAGGER_CLI_BIN_PREFIX = "dagger-"


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


@register_engine("docker-image")
@define
class Engine(BinEngine):
    cfg: Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start_sync(self) -> None:
        cache_dir = (
            Path(os.environ.get("XDG_CACHE_HOME", "~/.cache")).expanduser() / "dagger"
        )
        cache_dir.mkdir(mode=0o700, parents=True, exist_ok=True)

        os_, arch = get_platform()

        image = ImageRef(self.cfg.host.netloc + self.cfg.host.path)
        dagger_cli_bin_path = cache_dir / f"{DAGGER_CLI_BIN_PREFIX}{image.id}"
        if os_ == "windows":
            dagger_cli_bin_path = dagger_cli_bin_path.with_suffix(".exe")

        if not dagger_cli_bin_path.exists():
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

                docker_run_args = [
                    "docker",
                    "run",
                    "--rm",
                    "--entrypoint",
                    "/bin/cat",
                    image.ref,
                    f"/usr/bin/{DAGGER_CLI_BIN_PREFIX}{os_}-{arch}",
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
                        f"Failed to copy dagger cli binary: {e.stderr}"
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

                dagger_cli_bin_path = tmp_bin_path.rename(dagger_cli_bin_path)

            # garbage collection of old engine_session binaries
            for bin in cache_dir.glob(f"{DAGGER_CLI_BIN_PREFIX}*"):
                if bin != dagger_cli_bin_path:
                    bin.unlink()

        self._start(
            [dagger_cli_bin_path, "session"],
            default_dagger_runner_host=f"docker-image://{image.ref}",
        )
