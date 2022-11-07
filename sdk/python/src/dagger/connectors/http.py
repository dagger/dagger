import logging

from aiohttp import ClientTimeout
from attrs import define
from gql.transport import AsyncTransport, Transport
from gql.transport.aiohttp import AIOHTTPTransport
from gql.transport.requests import RequestsHTTPTransport

from .base import Connector, register_connector

logger = logging.getLogger(__name__)


@register_connector("http")
@define
class HTTPConnector(Connector):
    """Connect to dagger engine via HTTP"""

    @property
    def query_url(self) -> str:
        return f"{self.cfg.host.geturl()}/query"

    def make_transport(self) -> AsyncTransport:
        session_timeout = self.cfg.execute_timeout
        if isinstance(session_timeout, int):
            session_timeout = float(session_timeout)
        return AIOHTTPTransport(
            self.query_url,
            timeout=self.cfg.timeout,
            client_session_args={"timeout": ClientTimeout(total=session_timeout)},
        )

    def make_sync_transport(self) -> Transport:
        return RequestsHTTPTransport(
            self.query_url, timeout=self.cfg.timeout, retries=10
        )
