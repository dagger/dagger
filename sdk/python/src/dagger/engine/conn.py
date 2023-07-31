import logging
import os

import anyio

from dagger._progress import Progress
from dagger.config import Config, ConnectParams
from dagger.context import SyncResourceManager
from dagger.exceptions import ProvisionError

from .cli import CLISession
from .download import Downloader

logger = logging.getLogger(__name__)


class Engine(SyncResourceManager):
    """Start engine, provisioning if needed."""

    def __init__(self, cfg: Config, progress: Progress) -> None:
        super().__init__()
        self.cfg = cfg
        self.progress = progress

    def from_env(self) -> ConnectParams | None:
        if not (port := os.environ.get("DAGGER_SESSION_PORT")):
            return None
        if not (token := os.environ.get("DAGGER_SESSION_TOKEN")):
            msg = "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT"
            raise ProvisionError(msg)
        try:
            return ConnectParams(port=int(port), session_token=token)
        except ValueError as e:
            # only port is validated
            msg = f"Invalid DAGGER_SESSION_PORT: {port}"
            raise ProvisionError(msg) from e

    def from_cli(self) -> ConnectParams:
        # Only start progress if we are provisioning, not on active sessions
        # like `dagger run`.
        self.progress.start("Provisioning engine")

        cli_bin = os.environ.get("_EXPERIMENTAL_DAGGER_CLI_BIN") or Downloader().get(
            self.progress
        )

        self.progress.update("Creating new Engine session")
        with self.get_sync_stack() as stack:
            return stack.enter_context(CLISession(self.cfg, cli_bin))

    def start(self) -> ConnectParams:
        return self.from_env() or self.from_cli()

    async def __aenter__(self) -> ConnectParams:
        # TODO: Create proper async provisioning.
        return await anyio.to_thread.run_sync(self.start)

    async def __aexit__(self, *exc_details) -> None:
        # TODO: Create proper async provisioning.
        await anyio.to_thread.run_sync(self.__exit__, *exc_details)
