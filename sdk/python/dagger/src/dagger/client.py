import logging
from typing import NewType

from attrs import define, field
from gql import Client as GraphQLClient
from gql.client import AsyncClientSession, SyncClientSession
from gql.transport import AsyncTransport, Transport
from gql.transport.aiohttp import AIOHTTPTransport
from gql.transport.requests import RequestsHTTPTransport

SecretID = NewType("SecretID", str)
FSID = NewType("FSID", str)

logger = logging.getLogger(__name__)


@define
class Client:
    """
    GraphQL client proxy with both sync and async support via context managers
    """

    host: str = "localhost"
    port: int = 8080
    url: str = field(init=False)
    _wrapped: GraphQLClient | None = field(default=None, init=False)

    @url.default  # type: ignore
    def _set_url(self):
        return f"http://{self.host}:{self.port}/query"

    def _make_client(self, transport: Transport | AsyncTransport) -> GraphQLClient:
        return GraphQLClient(transport=transport, fetch_schema_from_transport=True)

    def _async_client(self) -> GraphQLClient:
        return self._make_client(AIOHTTPTransport(self.url))

    def _sync_client(self) -> GraphQLClient:
        return self._make_client(
            RequestsHTTPTransport(self.url, timeout=30, retries=10)
        )

    async def __aenter__(self) -> AsyncClientSession:
        self._wrapped = self._async_client()
        return await self._wrapped.__aenter__()

    async def __aexit__(self, *args, **kwargs):
        if self._wrapped is not None:
            await self._wrapped.__aexit__(*args, **kwargs)
            self._wrapped = None

    def __enter__(self) -> SyncClientSession:
        self._wrapped = self._sync_client()
        return self._wrapped.__enter__()  # type: ignore

    def __exit__(self, *args, **kwargs):
        if self._wrapped is not None:
            self._wrapped.__exit__(*args, **kwargs)
            self._wrapped = None
