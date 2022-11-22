import logging
import os
import subprocess
import time
from pathlib import Path

import anyio
from attrs import Factory, define, field

from dagger import Client
from dagger.exceptions import DaggerException

from .base import Config, register_connector
from .http import HTTPConnector

logger = logging.getLogger(__name__)


@define
class Engine:
    cfg: Config

    _proc: subprocess.Popen | None = field(default=None, init=False)

    def start(self) -> None:
        self._start(
            [f"{self.cfg.host.netloc}{self.cfg.host.path}" or "dagger-engine-session"]
        )

    def _start(
        self, base_args: list[str], default_dagger_runner_host: str = ""
    ) -> None:
        dagger_runner_host = os.environ.get(
            "DAGGER_RUNNER_HOST", default_dagger_runner_host
        )
        env = os.environ.copy()
        if dagger_runner_host != "":
            env["DAGGER_RUNNER_HOST"] = dagger_runner_host

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
            # read port number from first line of stdout
            port = int(self._proc.stdout.readline())
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


@register_connector("bin")
@define
class BinConnector(HTTPConnector):
    """Start engine session from a specified binary"""

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
        super().close_sync(exc_type)
        self.engine.stop(exc_type)


class ProvisionError(DaggerException):
    """Error while provisioning the Dagger engine."""
