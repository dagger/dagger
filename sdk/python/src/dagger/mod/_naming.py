"""Schema names the engine gives a module's own declarations.

The runtime registers Python names (``get_url``) and the engine turns them
into schema names (``getURL``). Calls the runtime builds itself, against its
own module's interfaces, have to use those schema names.

Modules at engine version v1.0.0 and later have their names formatted by the
engine's naming rules, which know acronyms (``URL``, ``HTTP``) a plain case
conversion can't. Rather than duplicate those rules here, the runtime asks the
engine to format its names (``Query.formatIdentifiers``), with the module's
own engine version, since that's what the runtime's session is served at.
Older modules don't have that API, and keep the conversion they always had.
"""

import dataclasses
import logging
import typing
from collections.abc import Awaitable, Callable, Iterable

import dagger
from dagger import dag
from dagger.mod._utils import to_camel_case

if typing.TYPE_CHECKING:
    from dagger.mod._resolver import ObjectType

logger = logging.getLogger(__package__)

CAMEL = "CAMEL"
PASCAL = "PASCAL"
SNAKE = "SNAKE"

Formatter: typing.TypeAlias = Callable[[list[str], str], Awaitable[list[str]]]
"""Format names in a casing (``CAMEL``, ``PASCAL`` or ``SNAKE``)."""


@dataclasses.dataclass(slots=True)
class SchemaNames:
    """The schema names of a module's interfaces and their members.

    Names not found here fall back to the conversion modules older than
    v1.0.0 get, which is also what an empty instance gives.
    """

    fields: dict[str, str] = dataclasses.field(default_factory=dict)
    """Function and argument names, from the name the runtime registers."""

    types: dict[type, str] = dataclasses.field(default_factory=dict)
    """Interface type names, from the interface's class."""

    def field_name(self, name: str) -> str:
        """The schema name of a function or argument."""
        return self.fields.get(name) or to_camel_case(name)

    def interface_name(self, main_name: str, proto: type) -> str:
        """The schema name of an interface declared in the module."""
        return self.types.get(proto) or main_name + proto.__name__


async def resolve_schema_names(
    main_name: str,
    interfaces: Iterable["ObjectType"],
    formatter: Formatter | None = None,
) -> SchemaNames | None:
    """Ask the engine for the schema names of the module's interfaces.

    Returns None if the engine doesn't format the module's names with its
    naming rules (modules older than v1.0.0).
    """
    interfaces = list(interfaces)
    if not interfaces:
        return None

    if formatter is None:
        if not await supports_identifiers():
            return None
        formatter = format_names

    names = sorted(
        {
            name
            for iface in interfaces
            for fn in iface.functions.values()
            for name in (fn.name, *(p.name for p in fn.parameters.values()))
            if name
        }
    )
    protos = [iface.cls for iface in interfaces]
    proto_names = [proto.__name__ for proto in protos]

    camel = await formatter(names, CAMEL) if names else []
    snake = await formatter([main_name, *proto_names], SNAKE)
    pascal = await formatter(
        [*proto_names, *(f"{main_name}_{name}" for name in proto_names)],
        PASCAL,
    )

    # Like the engine, namespace an interface with the module's name unless
    # its name already starts with the module's name, comparing words.
    main_snake, proto_snakes = snake[0], snake[1:]
    types = {}
    for i, proto in enumerate(protos):
        proto_snake = proto_snakes[i]
        if proto_snake == main_snake or proto_snake.startswith(f"{main_snake}_"):
            types[proto] = pascal[i]
        else:
            types[proto] = pascal[len(protos) + i]

    return SchemaNames(fields=dict(zip(names, camel, strict=True)), types=types)


async def supports_identifiers() -> bool:
    """Whether the session formats names with the engine's naming rules.

    The identifier API only exists in the API of engine version v1.0.0 and
    later, the same version from which the engine normalizes module names
    with its naming rules. The runtime's session is served at the module's
    engine version, and its client generated for it, so both have to have it.
    """
    if not hasattr(dag, "format_identifiers"):
        return False
    try:
        schema = await dag._ctx.conn.session.get_schema()  # noqa: SLF001
    except dagger.DaggerError:
        logger.debug("Failed to get the API schema", exc_info=True)
        return False
    return schema.query_type is not None and (
        "formatIdentifiers" in schema.query_type.fields
    )


async def format_names(names: list[str], casing: str) -> list[str]:
    """Format names with the engine's naming rules.

    A name the engine can't parse (non-ASCII, say) fails the whole batch.
    The engine leaves such a name unchanged when it normalizes module names,
    so do the same, formatting the rest one by one.
    """
    # Only in clients generated for v1.0.0 and later: look it up when called.
    casing_ = dagger.Casing(casing)
    try:
        return await dag.format_identifiers(names, casing_)
    except dagger.QueryError:
        logger.debug("Failed to format names in a batch", exc_info=True)

    return [await _format_name(name, casing_) for name in names]


async def _format_name(name: str, casing: "dagger.Casing") -> str:
    try:
        return await dag.identifier(name).format(casing)
    except dagger.QueryError:
        return name
