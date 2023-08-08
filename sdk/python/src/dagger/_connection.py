import contextlib
import logging

import dagger
from dagger import Config

from ._engine.conn import Engine, provision_engine
from ._managers import ResourceManager
from .client._session import SharedConnection

logger = logging.getLogger(__name__)


class Connection(ResourceManager):
    """Connect to a Dagger Engine.

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

    async def __aenter__(self):
        logger.debug("Establishing connection with isolated client")
        async with self.get_stack() as stack:
            engine = await Engine(self.cfg, stack).provision()
            conn = await engine.client_connection()
            client = dagger.Client.from_connection(conn)
            await engine.verify(client)
            logger.debug("Closing connection with isolated client")
            return client


# EXPERIMENTAL
@contextlib.asynccontextmanager
async def connection(config: Config | None = None):
    logger.debug("Establishing connection with shared client")
    async with provision_engine(config or Config()) as engine:
        conn = await engine.shared_client_connection()
        await engine.verify(dagger.client())
        yield conn
        logger.debug("Closing connection with shared client")


_shared = SharedConnection()
connect = _shared.connect
close = _shared.close


def closing():
    return contextlib.closing(_shared)
