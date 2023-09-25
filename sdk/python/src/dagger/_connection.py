import contextlib
import logging

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
            conn = engine.get_client_connection()
            return await engine.setup_client(conn)

    async def close(self):
        logger.debug("Closing connection with isolated client")
        await super().close()


@contextlib.asynccontextmanager
async def connection(config: Config | None = None):
    """Connect to a Dagger Engine using the global client.

    This is similar to :py:class:`dagger.Connection` but uses a
    global client so there's no need to pass around a client instance with this.

    Example::

        async def main():
            async with dagger.connection():
                ctr = dagger.container().from_("alpine")

            # Connection is closed when leaving the context manager's scope.


    You can stream the logs from the engine to see progress::

        import sys
        import anyio
        import dagger

        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.connection(cfg):
                ctr = dagger.container().from_("python:3.11.1-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.11.1

        anyio.run(main)

    Warning
    -------
    Experimental.
    """
    logger.debug("Establishing connection with shared client")
    async with provision_engine(config or Config()) as engine:
        conn = engine.get_shared_client_connection()
        await engine.setup_client(conn)
        yield conn
        logger.debug("Closing connection with shared client")


_shared = SharedConnection()
connect = _shared.connect
close = _shared.close


def closing():
    """Context manager that closes the global client's connection.

    It's an alternative to :py:func:`dagger.connection`, without automatic
    engine provisioning and has a lazy connection. The connection is only
    established when needed, i.e., when calling ``await`` on a client method.

    Example::

        import anyio
        import dagger

        async def main():
            async with dagger.closing():
                ctr = dagger.container().from_("python:3.11.1-alpine")
                # Connection is only established when needed.
                version = await ctr.with_exec(["python", "-V"]).stdout()

            # Connection is closed when leaving the context manager's scope.

            print(version)
            # Output: Python 3.11.1

        anyio.run(main)

    Warning
    -------
    Experimental.
    """
    return contextlib.aclosing(_shared)
