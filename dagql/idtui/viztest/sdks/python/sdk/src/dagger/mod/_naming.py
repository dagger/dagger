"""Schema names the engine gives a module's own declarations.

The runtime registers Python names (``get_url``, ``HttpFetcher``) and the
engine turns them into schema names (``getUrl``, ``NamingAppHTTPFetcher``) by
the naming rules of the version it names the module by. The calls the runtime
builds itself, through the module's own interfaces, have to use those schema
names: a wrong one is a fragment on a type that doesn't exist, a field the
interface doesn't have, or an unknown enum member.

So the runtime doesn't predict them: it asks the engine, which answers for the
module it is running (``CurrentModule.naming``) by the same rules it names the
module by, legacy strcase for a module declaring a version before the
identifier casing cut. One query per call, before anything is invoked, for
every name the calls through the module's interfaces can use.
"""

from __future__ import annotations

import dataclasses
import typing
from collections.abc import Awaitable, Callable, Iterable

from dagger.client._core import to_literal
from dagger.client._session import default_session
from dagger.mod._exceptions import ModuleError

if typing.TYPE_CHECKING:
    import enum

    from dagger.mod._resolver import ObjectType

Execute: typing.TypeAlias = Callable[[str], Awaitable[typing.Any]]
"""Runs a GraphQL query against the engine and returns its data."""


async def _execute(query: str) -> typing.Any:
    return await default_session().execute(query)


@dataclasses.dataclass(frozen=True, slots=True)
class ModuleNames:
    """The engine's names for the module's declarations, by declared name."""

    types: typing.Mapping[str, str] = dataclasses.field(default_factory=dict)
    """Namespaced schema names of the module's interfaces."""

    fields: typing.Mapping[str, str] = dataclasses.field(default_factory=dict)
    """Schema names of the interfaces' functions and arguments."""

    enum_members: typing.Mapping[str, str] = dataclasses.field(default_factory=dict)
    """Schema names of the members of the module's enums."""

    def type_name(self, name: str) -> str:
        """The schema name of an interface the module declares."""
        return self._get(self.types, name, "type")

    def field_name(self, name: str) -> str:
        """The schema name of an interface's function or argument."""
        return self._get(self.fields, name, "function or argument")

    def enum_member_name(self, name: str) -> str:
        """The schema name of a member of one of the module's enums."""
        return self._get(self.enum_members, name, "enum member")

    @staticmethod
    def _get(names: typing.Mapping[str, str], name: str, what: str) -> str:
        try:
            return names[name]
        except KeyError:
            # Every name a call through an interface uses is asked about
            # before anything runs; this one wasn't.
            msg = f"The engine wasn't asked for the schema name of {what} {name!r}"
            raise ModuleError(msg) from None


async def module_names(
    interfaces: Iterable[ObjectType],
    enums: Iterable[type[enum.Enum]],
    execute: Execute = _execute,
) -> ModuleNames:
    """Ask the engine how it names the module's interfaces and enums.

    One query: the interfaces' type names, their functions' and arguments'
    names, and the members of the module's enums, which calls through an
    interface send and receive.
    """
    interfaces = list(interfaces)
    types = sorted({iface.cls.__name__ for iface in interfaces})
    fields = sorted(
        {
            name
            for iface in interfaces
            for fn in iface.functions.values()
            for name in (fn.name, *(p.name for p in fn.parameters.values()))
        }
    )
    members = sorted({member.name for cls in enums for member in cls})
    asked = {
        "typeNames": types,
        "fieldNames": fields,
        "enumMemberNames": members,
    }
    selections = "\n".join(
        f"      {field}(names: {to_literal(names)})"
        for field, names in asked.items()
        if names
    )
    if not selections:
        return ModuleNames()
    data = await execute(
        "query ModuleNaming {\n"
        "  currentModule {\n"
        "    naming {\n"
        f"{selections}\n"
        "    }\n"
        "  }\n"
        "}"
    )
    naming = (data or {}).get("currentModule", {}).get("naming", {})

    def answers(field: str) -> dict[str, str]:
        names = asked[field]
        if not names:
            return {}
        got = naming.get(field)
        if not isinstance(got, list) or len(got) != len(names):
            msg = f"ModuleNaming.{field} returned {got!r} for {names!r}"
            raise ModuleError(msg)
        return dict(zip(names, got, strict=True))

    return ModuleNames(
        types=answers("typeNames"),
        fields=answers("fieldNames"),
        enum_members=answers("enumMemberNames"),
    )
