from dagger._config import Config
from dagger._connection import Connection
from dagger._managers import ResourceManager
from dagger.client.gen import Client


class Context(ResourceManager):
    def __init__(self, config: Config | None = None):
        super().__init__()
        self.config = config
        self._client: Client | None = None

    async def get_client(self) -> Client:
        """Get a dagger client, initiating connection only when requested."""
        if not self._client:
            async with self.get_stack() as stack:
                self._client = await stack.enter_async_context(Connection(self.config))
        return self._client
