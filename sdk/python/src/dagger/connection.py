import logging

from .connectors import Config, get_connector

logger = logging.getLogger(__name__)


class Connection:
    """
    Connect to a Dagger Engine.

    Example::

        async def main():
            async with dagger.Connection() as client:
                ctr = client.container().from_("alpine")

    You can stream the logs from the engine::

        import sys

        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.Connection(cfg) as client:
                ctr = client.container().from_("python:3.10.8-alpine")
                version = await ctr.exec(["python", "-V"]).stdout().contents()
                print(version)

                # Output: Python 3.10.8
    """

    def __init__(self, config: Config = None) -> None:
        if config is None:
            config = Config()
        self.connector = get_connector(config)

    async def __aenter__(self):
        return await self.connector.connect()

    async def __aexit__(self, exc_type, *args, **kwargs) -> None:
        await self.connector.close(exc_type)

    def __enter__(self):
        return self.connector.connect_sync()

    def __exit__(self, exc_type, *args, **kwargs) -> None:
        self.connector.close_sync(exc_type)
