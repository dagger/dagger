"""What a generated client hands the SDK: its target and its core digest."""

import contextlib
import dataclasses
import logging
import re
import threading
from collections.abc import Iterable, Iterator

from dagger._exceptions import QueryError, StaleClientError

logger = logging.getLogger(__name__)

GENERATE_HINT = "Run `dagger generate`."

# What the engine's validator answers when the schema lacks a field the
# bindings have. Anchored, because a resolver may quote the same words.
MISSING_FIELD = re.compile(
    r'^Cannot query field "([^"]*)" on type "([^"]*)"', re.IGNORECASE
)


@dataclasses.dataclass(frozen=True, slots=True)
class Target:
    """The module a client was generated for."""

    name: str
    ref: str
    pin: str | None = None


# Registration is a phase of the process, not of one task: a context
# variable would follow a child task out of the window and never reach a
# thread. The cost is that a concurrent session in this process, while a
# module registers, also sees a genuine mismatch downgraded to a warning.
_registering = 0
_registering_lock = threading.Lock()


@contextlib.contextmanager
def registering_types() -> Iterator[None]:
    """Mark the window in which the SDK registers a module's types.

    A stale client only warns here: a module with a client to itself has
    to run before that client can be regenerated.
    """
    global _registering  # noqa: PLW0603
    with _registering_lock:
        _registering += 1
    try:
        yield
    finally:
        with _registering_lock:
            _registering -= 1


def check_core(client: str, expected: str, installed: str) -> None:
    """Refuse a client generated against another core."""
    if expected == installed:
        return
    msg = (
        f"Client {client!r} was generated for core {expected}, "
        f"but the installed core is {installed}. {GENERATE_HINT}"
    )
    if _registering:
        logger.warning(msg)
        return
    raise StaleClientError(msg)


def missing_field(error: QueryError) -> "re.Match[str] | None":
    """The validation error for a field the schema lacks, with its two names."""
    # Validation fails before anything runs, so it carries no path, and says
    # it is validation in its code; any other error comes from something
    # that ran, whatever its message says.
    for e in error.errors:
        if (
            e.path is None
            and e.extensions.get("code") == "GRAPHQL_VALIDATION_FAILED"
            and (match := MISSING_FIELD.match(e.message))
        ):
            return match
    return None


def stale_client_error(
    error: QueryError, targets: Iterable[Target]
) -> StaleClientError | None:
    """The error a missing field means once the module was loaded."""
    missing = missing_field(error)
    if missing is None:
        return None
    # The name and the address both: a module served under another name
    # than the one generated against lands here too, and the address is
    # what the user has to look at.
    clients = [_describe(t) for t in sorted(targets, key=lambda t: t.name)]
    which = (
        f"The client {clients[0]} is"
        if len(clients) == 1
        else f"The clients {', '.join(clients)} are"
    )
    msg = f"{missing.string} {which} out of date. {GENERATE_HINT}"
    return StaleClientError(msg)


def _describe(target: Target) -> str:
    where = f"{target.ref} at {target.pin}" if target.pin else target.ref
    return f"{target.name!r} from {where}"
