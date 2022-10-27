import logging
import os
import subprocess
import sys
from subprocess import DEVNULL, CalledProcessError, Popen, run

from attrs import define, field
from gql.client import AsyncClientSession, SyncClientSession

from .client import Client

logger = logging.getLogger(__name__)


@define
class Engine:
    port: int = 8080
    workdir: str = field(factory=lambda: os.environ.get("DAGGER_WORKDIR", os.getcwd()))
    config_path: str = field(
        factory=lambda: os.environ.get("DAGGER_CONFIG", "./cloak.yaml")
    )
    client: Client = field(init=False)
    _proc: subprocess.Popen | None = field(init=False, default=None)

    @client.default  # type: ignore
    def _set_client(self) -> Client:
        return Client(port=self.port)

    def _spawn_args(self) -> list[str]:
        return [
            "cloak",
            "dev",
            "--workdir",
            self.workdir,
            "--port",
            str(self.port),
            "-p",
            self.config_path,
        ]

    def _check_dagger_version(self) -> None:
        try:
            run(["cloak", "dev", "--help"], stdout=DEVNULL, stderr=DEVNULL, check=True)
        except CalledProcessError as e:
            logger.error(
                "⚠️  Please ensure that cloak binary in $PATH is v0.3.0 or newer."
            )
            sys.exit(127)

    def __enter__(self) -> SyncClientSession:
        self._check_dagger_version()
        self._proc = Popen(self._spawn_args())
        # FIXME: catch errors while establishing connection
        return self.client.__enter__()

    def __exit__(self, *args, **kwargs):
        self.client.__exit__(*args, **kwargs)
        self._cleanup()

    async def __aenter__(self) -> AsyncClientSession:
        self._check_dagger_version()
        # FIXME: replace with async subprocess
        self._proc = Popen(self._spawn_args())
        # FIXME: catch errors while establishing connection
        return await self.client.__aenter__()

    async def __aexit__(self, *args, **kwargs):
        await self.client.__aexit__(*args, **kwargs)
        self._cleanup()

    def _cleanup(self):
        assert self._proc is not None
        self._proc.terminate()
        # Gives 5 seconds for the process to terminate properly
        self._proc.wait(timeout=3)
        if self._proc.poll() is None:
            self._proc.kill()
