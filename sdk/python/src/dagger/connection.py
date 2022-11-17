import logging

from .connectors import Config, get_connector

logger = logging.getLogger(__name__)


class Connection:
    """Connect to a Dagger Engine."""

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
