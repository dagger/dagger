import logging
import sys
from asyncio.subprocess import Process
from subprocess import DEVNULL, CalledProcessError

import anyio
from aiohttp import ClientTimeout
from attrs import Factory, define, field
from gql.transport import AsyncTransport, Transport
from gql.transport.aiohttp import AIOHTTPTransport
from gql.transport.requests import RequestsHTTPTransport

from dagger import Client

from .base import Config, Connector, register_connector

logger = logging.getLogger(__name__)


@define
class Engine:
    cfg: Config

    _proc: Process | None = field(default=None, init=False)

    async def assert_version(self) -> None:
        try:
            await anyio.run_process(
                ["cloak", "dev", "--help"], stdout=DEVNULL, stderr=DEVNULL, check=True
            )
        except CalledProcessError:
            logger.error(
                "⚠️  Please ensure that cloak binary in $PATH is v0.3.0 or newer."
            )
            # FIXME: make sure resources are being cleaned up correctly
            sys.exit(127)

    async def start(self) -> None:
        await self.assert_version()
        args = ["cloak", "dev"]
        if self.cfg.workdir:
            args.extend(["--workdir", str(self.cfg.workdir.absolute())])
        if self.cfg.host.port:
            args.extend(["--port", str(self.cfg.host.port)])
        if self.cfg.config_path:
            args.extend(["-p", str(self.cfg.config_path.absolute())])
        self._proc = await anyio.open_process(args)

    async def stop(self) -> None:
        assert self._proc is not None
        # FIXME: make sure signals from OS are being handled
        self._proc.terminate()
        # Gives 5 seconds for the process to terminate properly
        # FIXME: timeout
        await self._proc.wait()
        # FIXME: kill?


@register_connector("http")
@define
class HTTPConnector(Connector):
    """Connect to dagger engine via HTTP"""

    engine: Engine = Factory(lambda self: Engine(self.cfg), takes_self=True)

    @property
    def query_url(self) -> str:
        return f"{self.cfg.host.geturl()}/query"

    async def connect(self) -> Client:
        if self.cfg.host.hostname == "localhost":
            await self.provision()
        return await super().connect()

    async def provision(self) -> None:
        # FIXME: only provision if port is not in use
        # FIXME: handle cancellation, retries and timeout
        # FIXME: handle errors during provisioning
        # await self.engine.start()
        ...

    async def close(self) -> None:
        # FIXME: need exit stack?
        await super().close()
        # await self.engine.stop()

    def connect_sync(self) -> Client:
        # FIXME: provision engine in sync
        return super().connect_sync()

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
