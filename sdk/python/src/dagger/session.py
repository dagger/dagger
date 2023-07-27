import logging

import httpx
from gql.client import AsyncClientSession
from gql.client import Client as GraphQLClient
from gql.transport.exceptions import (
    TransportProtocolError,
    TransportQueryError,
    TransportServerError,
)

from dagger.exceptions import ClientConnectionError

from .config import Config, ConnectParams
from .context import ResourceManager
from .transport.httpx import HTTPXAsyncTransport

logger = logging.getLogger(__name__)


class Session(ResourceManager):
    """Establish a GraphQL client connection to the engine."""

    def __init__(self, conn: ConnectParams, cfg: Config):
        super().__init__()
        self.conn = conn
        self.cfg = cfg

    async def __aenter__(self) -> AsyncClientSession:
        transport = HTTPXAsyncTransport(
            self.conn.url,
            timeout=self.cfg.execute_timeout,
            auth=(self.conn.session_token, ""),
        )
        client = GraphQLClient(
            transport=transport,
            fetch_schema_from_transport=True,
            # Don't timeout with the event loop. This setting is only for AsyncTransport
            # and uses `asyncio.wait_for` which is not compatible with other event loops
            # (e.g., Trio). Catching the TimeoutError would also be more complicated,
            # unless *gql* adopts AnyIO in the future, but still only on *async*.
            # Since we're using *httpx* as the transport for both *async* and *sync*, we
            # can use that project's Timeout instead for both environments.
            execute_timeout=None,
        )
        async with self.get_stack() as stack:
            try:
                session = await stack.enter_async_context(client)
            except httpx.RequestError as e:
                msg = f"Could not make request: {e}"
                raise ClientConnectionError(msg) from e
            except (TransportProtocolError, TransportServerError) as e:
                msg = f"Got unexpected response from engine: {e}"
                raise ClientConnectionError(msg) from e
            except TransportQueryError as e:
                # Only query during connection is the introspection query
                # for building the schema.
                msg = str(e)
                # Extract only the error message.
                if e.errors and "message" in e.errors[0]:
                    msg = e.errors[0]["message"].strip()
                msg = f"Failed to build schema from introspection query: {msg}"
                raise ClientConnectionError(msg) from e
            return session
