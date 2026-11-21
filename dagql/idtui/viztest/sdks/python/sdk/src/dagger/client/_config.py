import dataclasses
import functools
import typing

if typing.TYPE_CHECKING:
    import httpx


@dataclasses.dataclass(slots=True, kw_only=True)
class Retry:
    """Retry policy for talking to the Dagger API server.

    Parameters
    ----------
    connect:
        Let the HTTP transport retry failures to connect to the server,
        up to ``MAX_ATTEMPTS`` times.
    execute:
        Re-send requests that fail with a transport error, up to
        ``MAX_ATTEMPTS`` times, with an exponential backoff capped at
        ``MAX_BACKOFF_SECONDS``. Every API call is cached on its inputs,
        so re-sending is safe.
    """

    connect: bool = True
    execute: bool = True


@functools.cache
def _timeout_class() -> "type[httpx.Timeout]":
    # Defined on first use: httpx costs about 28 ms to import, and a module
    # call that makes no API request never needs it.
    import httpx

    class Timeout(httpx.Timeout):
        """
        Timeout configuration.

        Examples::

            Timeout(None)  # No timeouts.
            Timeout(5.0)  # 5s timeout on all operations.
            Timeout(None, connect=5.0)  # 5s timeout on connect, no other timeouts.
            Timeout(5.0, connect=10.0)  # 10s timeout on connect. 5s timeout elsewhere.
            Timeout(5.0, pool=None)  # No timeout on acquiring connection from pool.
        """

        @classmethod
        def default(cls) -> "Timeout":
            return cls(None, connect=10.0)

    Timeout.__module__ = __name__
    Timeout.__qualname__ = "Timeout"
    return Timeout


def __getattr__(name: str) -> typing.Any:
    if name == "Timeout":
        return _timeout_class()
    msg = f"module {__name__!r} has no attribute {name!r}"
    raise AttributeError(msg)


@dataclasses.dataclass(slots=True, kw_only=True)
class ConnectConfig:
    timeout: "httpx.Timeout | None" = dataclasses.field(
        default_factory=lambda: _timeout_class().default()
    )
    retry: Retry | None = dataclasses.field(default_factory=Retry)
