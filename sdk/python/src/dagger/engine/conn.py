import logging
import os

import anyio

from dagger.config import Config, ConnectParams
from dagger.context import SyncResourceManager
from dagger.exceptions import ProvisionError

from .cli import CLISession
from .download import Downloader

logger = logging.getLogger(__name__)


class Engine(SyncResourceManager):
    """Start engine, provisioning if needed."""

    def __init__(self, cfg: Config) -> None:
        super().__init__()
        self.cfg = cfg

    def from_env(self) -> ConnectParams | None:
        if not (port := os.environ.get("DAGGER_SESSION_PORT")):
            return None
        if not (token := os.environ.get("DAGGER_SESSION_TOKEN")):
            msg = "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT"
            raise ProvisionError(msg)
        try:
            return ConnectParams(port, token)
        except ValueError as e:
            # only port is validated
            msg = f"Invalid DAGGER_SESSION_PORT: {port}"
            raise ProvisionError(msg) from e

    def from_cli(self) -> ConnectParams:
        cli_bin = os.environ.get("_EXPERIMENTAL_DAGGER_CLI_BIN")
        if not cli_bin:
            cli_bin = Downloader().get()
        with self.get_sync_stack() as stack:
            return stack.enter_context(CLISession(self.cfg, cli_bin))

    def __enter__(self) -> ConnectParams:
        return self.from_env() or self.from_cli()

    async def __aenter__(self) -> ConnectParams:
        # FIXME: Create proper async provisioning later.
        # This is just to support sync faster.
        return await anyio.to_thread.run_sync(self.__enter__)

    async def __aexit__(self, *exc_details) -> None:
        # FIXME: Create proper async provisioning later.
        # This is just to support sync faster.
        await anyio.to_thread.run_sync(self.__exit__, *exc_details)
