from __future__ import annotations

import atexit
import functools
import logging
import os
import threading
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any, Self

import anyio
from opentelemetry import propagate

from dagger import telemetry
from dagger._exceptions import (
    ClientConnectionError,
    ClientLoadError,
    DaggerError,
    TransportError,
    _query_error_from_response,
)
from dagger._managers import ResourceManager
from dagger.client._config import ConnectConfig

if TYPE_CHECKING:
    import httpx

    from dagger.client._descriptor import Target

logger = logging.getLogger(__name__)

# Safe to retry: every API call is cached on its inputs.
MAX_ATTEMPTS = 5
MAX_BACKOFF_SECONDS = 2.0


@dataclass(slots=True, kw_only=True)
class ConnectParams:
    """Options for making a session connection. For internal use only."""

    port: int
    session_token: str
    url: httpx.URL = field(init=False)

    def __post_init__(self):
        self.port = int(self.port)
        if self.port < 1:
            msg = f"Invalid port value: {self.port}"
            raise ValueError(msg)
        import httpx

        self.url = httpx.URL(f"http://127.0.0.1:{self.port}/query")

    @classmethod
    def from_env(cls) -> ConnectParams | None:
        if not (port := os.getenv("DAGGER_SESSION_PORT")):
            return None
        if not (token := os.getenv("DAGGER_SESSION_TOKEN")):
            msg = "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT"
            raise ClientConnectionError(msg)
        try:
            return cls(port=int(port), session_token=token)
        except ValueError as e:
            # only port is validated
            msg = f"Invalid DAGGER_SESSION_PORT: {port}"
            raise ClientConnectionError(msg) from e


@functools.cache
def _telemetry_transport() -> type[httpx.AsyncHTTPTransport]:
    # Defined on first use: httpx costs about 28 ms to import, and a module
    # call that makes no API request never needs it.
    import httpx

    class TelemetryTransport(httpx.AsyncHTTPTransport):
        async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
            propagate.inject(request.headers)
            return await super().handle_async_request(request)

    return TelemetryTransport


class ClientSession(ResourceManager):
    """HTTP session to the engine's GraphQL API.

    Queries go as text: the generated client knows its schema, so nothing is
    fetched or validated on connect.
    """

    def __init__(self, conn: ConnectParams, cfg: ConnectConfig | None = None):
        super().__init__()

        if cfg is None:
            cfg = ConnectConfig()

        self.conn = conn
        self.cfg = cfg
        self._client: httpx.AsyncClient | None = None

    def _make_client(self) -> httpx.AsyncClient:
        import httpx

        retry = self.cfg.retry
        return httpx.AsyncClient(
            auth=(self.conn.session_token, ""),
            timeout=self.cfg.timeout,
            transport=_telemetry_transport()(
                retries=MAX_ATTEMPTS if retry and retry.connect else 0,
                # Plain HTTP on loopback: the default TLS context costs ~70ms
                # per process start and is never used.
                verify=False,
            ),
        )

    async def __aenter__(self) -> Self:
        await self.start()
        return self

    async def start(self) -> httpx.AsyncClient:
        if self._client:
            return self._client

        async with self.get_stack() as stack:
            logger.debug("Establishing client session to GraphQL server")
            self._client = await stack.enter_async_context(self._make_client())
            return self._client

    def has_session(self):
        return self._client is not None

    async def execute(self, query: str) -> Any:
        """Send a query and return its data."""
        import httpx

        client = await self.start()
        retry = self.cfg.retry
        attempts = MAX_ATTEMPTS if retry and retry.execute else 1

        for attempt in range(1, attempts + 1):
            try:
                response = await client.post(self.conn.url, json={"query": query})
            except httpx.TransportError as e:
                if attempt == attempts:
                    raise TransportError(_transport_message(e)) from e
                delay = min(0.1 * 2 ** (attempt - 1), MAX_BACKOFF_SECONDS)
                logger.debug("Request failed (%s), retrying in %.1fs", e, delay)
                await anyio.sleep(delay)
            except RuntimeError as e:
                # httpx raises this when the client has already been closed.
                msg = (
                    "Connection to engine has been closed. Make sure you're "
                    "calling the API within a `dagger.connection()` context."
                )
                raise TransportError(msg) from e
            else:
                return _read_response(response, query)

        msg = "Failed to execute request"
        raise TransportError(msg)

    async def close(self) -> None:
        logger.debug("Closing client session to GraphQL server")
        await super().close()
        self._client = None


def _transport_message(e: httpx.TransportError) -> str:
    import httpx

    if isinstance(e, httpx.TimeoutException):
        return (
            "Request timed out. Try setting a higher timeout value for this connection."
        )
    if msg := str(e):
        return f"Failed to execute request: {msg}"
    return "Failed to execute request"


def _read_response(response: httpx.Response, query: str) -> Any:
    try:
        body = response.json()
    except ValueError as e:
        msg = _unexpected(response)
        raise TransportError(msg) from e

    if not isinstance(body, dict):
        raise TransportError(_unexpected(response))

    if errors := body.get("errors"):
        if err := _query_error_from_response(errors, query):
            raise err
        msg = f"Unexpected error response from engine: {errors!r}"
        raise TransportError(msg)

    if response.status_code != 200:  # noqa: PLR2004 - httpx.codes.OK
        raise TransportError(_unexpected(response))

    return body.get("data")


def _unexpected(response: httpx.Response) -> str:
    return (
        f"Unexpected response from engine: {response.status_code} {response.text[:200]}"
    )


class BaseConnection:
    session: ClientSession
    # Kept on the connection so the session dies with it, and so every
    # execution over one connection shares one load memo and name guard.
    _as_session: Session | None = None

    async def _ready(self) -> ClientSession:
        """The session, once there is an engine behind it."""
        return self.session

    async def connect(self) -> Self:
        await (await self._ready()).start()
        return self

    async def close(self) -> None:
        # The transport forgets its served modules; so must the session.
        if self._as_session is not None:
            self._as_session.forget()
        await self.session.close()

    async def aclose(self) -> None:
        await self.close()

    def __await__(self):
        return self.connect().__await__()

    async def __aenter__(self) -> Self:
        telemetry.initialize()
        return await self.connect()

    async def __aexit__(self, *_) -> None:
        await self.close()


class SingleConnection(BaseConnection):
    """Establish a GraphQL client connection to the Dagger API server."""

    def __init__(self, conn: ConnectParams, cfg: ConnectConfig | None = None):
        self.session = ClientSession(conn, cfg)


class SharedConnection(BaseConnection):
    """Establish a GraphQL client connection to the Dagger API server.

    Uses a lazy and shared connection.
    """

    _instance: Self | None = None
    _session: ClientSession | None = None
    _params: ConnectParams | None = None
    _cfg: ConnectConfig
    # Set while this process runs an engine it provisioned itself.
    _stop_engine: Callable[[], None] | None = None
    _provisioning: anyio.Lock | None = None

    def __new__(cls):
        if not cls._instance:
            cls._instance = super().__new__(cls)
            cls._cfg = ConnectConfig()
        return cls._instance

    def __init__(self) -> None:
        # This is a singleton class, so we don't want to initialize.
        ...

    def with_params(self, params: ConnectParams) -> Self:
        """Set the connection params."""
        if self._session:
            logger.warning(
                "Cannot set connection params after connection already started"
            )
        else:
            self._params = params
        return self

    def with_config(self, cfg: ConnectConfig) -> Self:
        """Set the connection config."""
        if self._session:
            logger.warning(
                "Cannot set connection config after connection already started"
            )
        else:
            self._cfg = cfg
        return self

    @property
    def session(self) -> ClientSession:
        if not self._session:
            logger.debug("Configuring shared connection to GraphQL server")

            # Delay checking the environment until we actually need it.
            if not self._params:
                self._params = ConnectParams.from_env()

            if not self._params:
                msg = "No active engine session to connect to"
                raise ClientConnectionError(msg)

            self._session = ClientSession(self._params, self._cfg)
        return self._session

    async def _ready(self) -> ClientSession:
        """The session, provisioning an engine if nothing gave one.

        A module and `dagger run` put a session in the environment, and
        `dagger.connection()` passes its own, so both come first: a module
        must never try to provision. Only a plain program with no connection
        handling gets here with neither.
        """
        if not self._session and not self._params:
            self._params = ConnectParams.from_env()
            if not self._params:
                await self._provision()
        return self.session

    async def _provision(self) -> None:
        if self._provisioning is None:
            self._provisioning = anyio.Lock()
        async with self._provisioning:
            if self._params:
                return
            if _module_runtime:
                # The engine gives a module its session; one that lacks it
                # must say so, not download a CLI in the module's container.
                msg = "No active engine session to connect to"
                raise ClientConnectionError(msg)
            try:
                # Not at import: an older module runtime has no provisioning.
                from dagger.provisioning._config import Config
                from dagger.provisioning._engine import provision_default_session
            except ModuleNotFoundError as e:
                if not (e.name or "").startswith("dagger.provisioning"):
                    raise
                msg = "No active engine session to connect to"
                raise ClientConnectionError(msg) from e

            cfg = Config(timeout=self._cfg.timeout, retry=self._cfg.retry)
            self._params, self._stop_engine = await provision_default_session(cfg)
            # The engine is a subprocess, and closing it is sync, so it can
            # run at exit, after the program's event loop is gone.
            atexit.register(self.stop_engine)

    def stop_engine(self) -> None:
        """End the engine this process provisioned, if it did."""
        stop, self._stop_engine = self._stop_engine, None
        if stop is not None:
            atexit.unregister(self.stop_engine)
            self._params = None
            stop()

    def is_connected(self) -> bool:
        return self._session is not None and self._session.has_session()

    async def close(self) -> None:
        if self._session:
            await super().close()
            self._session = None
        if self._stop_engine is not None:
            await anyio.to_thread.run_sync(self.stop_engine)


@dataclass(slots=True)
class _Load:
    target: Target
    lock: anyio.Lock = field(default_factory=anyio.Lock)
    done: bool = False


class Session(BaseConnection):
    """A connection to one engine, and what has been loaded into it.

    Owns the connection, the query transport and the load memo. It has no
    API field: a client is the way in.
    """

    def __init__(self, connection: BaseConnection | None = None) -> None:
        # No connection is the shared one, which a module and `dagger run`
        # set up: the session the generated global client builds is over it.
        self.connection = SharedConnection() if connection is None else connection
        self._loads: dict[str, _Load] = {}

    @property
    def session(self) -> ClientSession:  # type: ignore[override]
        return self.connection.session

    async def _ready(self) -> ClientSession:
        return await self.connection._ready()  # noqa: SLF001

    async def connect(self) -> Self:
        await self.connection.connect()
        return self

    async def close(self) -> None:
        self.forget()
        await self.connection.close()

    def forget(self) -> None:
        """Drop the load memo: a new engine behind the connection has nothing."""
        self._loads.clear()

    async def execute(self, query: str) -> Any:
        return await (await self._ready()).execute(query)

    def __getattr__(self, name: str) -> Any:
        # Only reached for a name the session lacks, and the likely ask is
        # an API field of the global client that dag used to be. Telling a
        # core field from a client would take importing core, so the message
        # names both.
        msg = f"{type(self).__name__!r} object has no attribute {name!r}"
        if not name.startswith("_"):
            msg += (
                f". The API is on the clients now: core().{name}() for a core "
                "field (from dagger_clients.core import core), "
                f"or {name}() from the client's package for a client. "
                f"To keep dag.{name}() while migrating, set "
                "global-client = true under [tool.dagger] and run dagger generate."
            )
        raise AttributeError(msg, name=name, obj=self)

    async def load(self, target: Target) -> None:
        """Serve the module a target names, once per session."""
        entry = self._loads.setdefault(target.name, _Load(target))
        if entry.target != target:
            # Whole descriptors: the two may differ only by pin, and the
            # first is held from its first attempt, loaded or not.
            msg = (
                f"This session already holds {entry.target!r} "
                f"and cannot also take {target!r}"
            )
            raise ClientLoadError(msg, target=target)
        async with entry.lock:
            if entry.done:
                return
            # The loader builds its query with Context, which imports this
            # module, so it can only be reached from inside a function.
            from dagger.client._load import load_target

            try:
                await load_target(self, target)
            except DaggerError as e:
                msg = f"Failed to load client {target.name!r} from {target.ref!r}: {e}"
                raise ClientLoadError(msg, target=target) from e
            entry.done = True


_module_runtime = False


def mark_module_runtime() -> None:
    """Say this process serves a module, so it never provisions an engine.

    The module entrypoints call it first. A module's session comes from the
    engine that runs it; without this, only that session being there would
    keep the default session from provisioning one.
    """
    global _module_runtime  # noqa: PLW0603
    _module_runtime = True


_default: Session | None = None
# Sessions are looked up from threads too, before any event loop exists.
_sessions_lock = threading.Lock()


def _no_default() -> Session | None:
    return None


_find_default: Callable[[], Session | None] = _no_default


def set_default_finder(find: Callable[[], Session | None]) -> None:
    """Say where to find a default session before making one.

    Only the temporary global client needs this: its ``dag`` is a Session,
    and it has to be the default one, so that a client called without
    ``session=`` and ``dagger.connection()`` share its loads.
    """
    global _find_default  # noqa: PLW0603
    _find_default = find


def default_session() -> Session:
    """The one session per process, over the shared connection."""
    global _default  # noqa: PLW0603
    if _default is None:
        # Outside the lock: finding it can import generated code, which must
        # not run while other threads wait on the lock.
        found = _find_default()
        with _sessions_lock:
            if _default is None:
                _default = Session() if found is None else found
    return _default


def as_session(conn: BaseConnection) -> Session:
    """The session a connection belongs to."""
    if isinstance(conn, Session):
        return conn
    if isinstance(conn, SharedConnection):
        return default_session()
    with _sessions_lock:
        if conn._as_session is None:  # noqa: SLF001
            conn._as_session = Session(conn)  # noqa: SLF001
        return conn._as_session  # noqa: SLF001
