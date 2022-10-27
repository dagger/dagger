import logging

from attrs import define, field
from gql import Client as GraphQLClient
from gql.client import AsyncClientSession, SyncClientSession
from gql.transport import AsyncTransport, Transport
from gql.transport.aiohttp import AIOHTTPTransport
from gql.transport.requests import RequestsHTTPTransport

logger = logging.getLogger(__name__)


@define
class Client:
    """
    GraphQL client proxy with both sync and async support via context managers
    """

    host: str = "localhost"
    port: int = 8080
    timeout: int | None = 30
    url: str = field(init=False)
    _wrapped: GraphQLClient | None = field(default=None, init=False)

    @url.default
    def _set_url(self):
        return f"http://{self.host}:{self.port}/query"

    def _make_client(self, transport: Transport | AsyncTransport) -> GraphQLClient:
        return GraphQLClient(transport=transport, fetch_schema_from_transport=True)

    def _async_client(self) -> GraphQLClient:
        return self._make_client(AIOHTTPTransport(self.url, timeout=self.timeout))

    def _sync_client(self) -> GraphQLClient:
        return self._make_client(RequestsHTTPTransport(self.url, timeout=self.timeout, retries=10))

    async def __aenter__(self) -> AsyncClientSession:
        self._wrapped = self._async_client()
        return await self._wrapped.__aenter__()

    async def __aexit__(self, *args, **kwargs):
        assert self._wrapped is not None
        await self._wrapped.__aexit__(*args, **kwargs)
        self._wrapped = None

    def __enter__(self) -> SyncClientSession:
        self._wrapped = self._sync_client()
        return self._wrapped.__enter__()

    def __exit__(self, *args, **kwargs):
        assert self._wrapped is not None
        self._wrapped.__exit__(*args, **kwargs)
        self._wrapped = None
