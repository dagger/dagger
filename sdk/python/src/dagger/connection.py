import logging
import warnings

from gql.client import AsyncClientSession

from dagger import Client, Config

from ._progress import Progress
from .context import ResourceManager
from .engine._version import CLI_VERSION
from .engine import Engine
from .exceptions import QueryError, VersionMismatch
from .session import Session

logger = logging.getLogger(__name__)


class Connection(ResourceManager):
    """
    Connect to a Dagger Engine.

    Example::

        async def main():
            async with dagger.Connection() as client:
                ctr = client.container().from_("alpine")

    You can stream the logs from the engine to see progress::

        import sys
        import anyio
        import dagger

        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.Connection(cfg) as client:
                ctr = client.container().from_("python:3.11.1-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.11.1

        anyio.run(main)
    """

    def __init__(self, config: Config | None = None) -> None:
        super().__init__()
        self.cfg = config or Config()
        self._progress = Progress(self.cfg.console)
        self._engine = Engine(self.cfg, self._progress)

    async def start(self) -> AsyncClientSession:
        async with self.get_stack() as stack:
            progress = await stack.enter_async_context(self._progress)
            conn = await stack.enter_async_context(self._engine)

            progress.update("Establishing connection to Engine")
            session = await stack.enter_async_context(Session(conn, self.cfg))

            # If log_output is set, we don't need to show any more progress.
            if self.cfg.log_output:
                progress.stop()
            else:
                progress.update("Running pipelines")

            return session

    async def aclose(self) -> None:
        await self.stack.aclose()

    async def __aenter__(self):
        client = Client.from_session(await self.start())

        try:
            if not await client.check_version_compatibility(CLI_VERSION) and (
                msg := self._engine.version_mismatch_msg
            ):
                warnings.warn(msg, VersionMismatch, stacklevel=2)
        except QueryError as e:
            logger.warning("Failed to check Dagger engine version compatibility: %s", e)

        return client
