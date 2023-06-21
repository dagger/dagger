from dagger.api.gen import Client
from dagger.config import Config
from dagger.connection import Connection
from dagger.context import ResourceManager


class Context(ResourceManager):
    def __init__(self, config: Config | None = None):
        super().__init__()
        self.config = config
        self._client: Client | None = None

    def __attrs_pre_init__(self):
        super().__init__()

    async def get_client(self) -> Client:
        """Get a dagger client, initiating connection only when requested."""
        if not self._client:
            async with self.get_stack() as stack:
                self._client = await stack.enter_async_context(Connection(self.config))
        return self._client
