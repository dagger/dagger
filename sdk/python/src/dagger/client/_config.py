import dataclasses
from collections.abc import Callable
from typing import Any, TypeVar

import httpx

_CallableT = TypeVar("_CallableT", bound=Callable[..., Any])
_Decorator = Callable[[_CallableT], _CallableT]


@dataclasses.dataclass(slots=True, kw_only=True)
class Retry:
    """Retry parameters for connecting to the Dagger API server."""

    connect: bool | _Decorator = True
    execute: bool | _Decorator = True


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


@dataclasses.dataclass(slots=True, kw_only=True)
class ConnectConfig:
    timeout: Timeout | None = dataclasses.field(default_factory=Timeout.default)
    retry: Retry | None = dataclasses.field(default_factory=Retry)
