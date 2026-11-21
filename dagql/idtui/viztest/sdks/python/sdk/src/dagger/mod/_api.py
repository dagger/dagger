"""The engine API that module support calls, on the raw query builder.

The SDK files can't import generated bindings, so the few fields needed to
register types and to answer a function call are selected by name here.
Arguments and their defaults follow the generated bindings, so the queries
are the same.
"""

from __future__ import annotations

import dataclasses
from collections.abc import Sequence
from typing import Self

from dagger.client._core import Arg, Context, EnumName
from dagger.client.base import Type


class _Object(Type):
    async def id(self) -> str:
        return await self._select("id", []).execute(str)

    def _with(self, field: str, *args: Arg) -> Self:
        return type(self)(self._select(field, args))


class TypeDef(_Object):
    def with_optional(self, optional: bool) -> Self:
        return self._with("withOptional", Arg("optional", optional))

    def with_kind(self, kind: str) -> Self:
        return self._with("withKind", Arg("kind", EnumName(kind)))

    def with_list_of(self, element_type: TypeDef) -> Self:
        return self._with("withListOf", Arg("elementType", element_type))

    def with_scalar(self, name: str, *, description: str | None = "") -> Self:
        return self._with(
            "withScalar",
            Arg("name", name),
            Arg("description", description, ""),
        )

    def with_enum(self, name: str, *, description: str | None = "") -> Self:
        return self._with(
            "withEnum",
            Arg("name", name),
            Arg("description", description, ""),
        )

    def with_enum_member(
        self,
        name: str,
        *,
        value: str | None = "",
        description: str | None = "",
        deprecated: str | None = None,
    ) -> Self:
        return self._with(
            "withEnumMember",
            Arg("name", name),
            Arg("value", value, ""),
            Arg("description", description, ""),
            Arg("deprecated", deprecated, None),
        )

    def with_interface(self, name: str, *, description: str | None = "") -> Self:
        return self._with(
            "withInterface",
            Arg("name", name),
            Arg("description", description, ""),
        )

    def with_object(
        self,
        name: str,
        *,
        description: str | None = "",
        deprecated: str | None = None,
    ) -> Self:
        return self._with(
            "withObject",
            Arg("name", name),
            Arg("description", description, ""),
            Arg("deprecated", deprecated, None),
        )

    def with_field(
        self,
        name: str,
        type_def: TypeDef,
        *,
        description: str | None = "",
        deprecated: str | None = None,
    ) -> Self:
        return self._with(
            "withField",
            Arg("name", name),
            Arg("typeDef", type_def),
            Arg("description", description, ""),
            Arg("deprecated", deprecated, None),
        )

    def with_function(self, function: Function) -> Self:
        return self._with("withFunction", Arg("function", function))

    def with_constructor(self, function: Function) -> Self:
        return self._with("withConstructor", Arg("function", function))

    def with_collection(self) -> Self:
        return self._with("withCollection")

    def with_collection_keys(self, name: str) -> Self:
        return self._with("withCollectionKeys", Arg("name", name))

    def with_collection_get(self, name: str) -> Self:
        return self._with("withCollectionGet", Arg("name", name))

    def with_collection_delta(self, name: str) -> Self:
        return self._with("withCollectionDelta", Arg("name", name))


class Function(_Object):
    def with_description(self, description: str) -> Self:
        return self._with("withDescription", Arg("description", description))

    def with_cache_policy(
        self, policy: str, *, time_to_live: str | None = None
    ) -> Self:
        return self._with(
            "withCachePolicy",
            Arg("policy", EnumName(policy)),
            Arg("timeToLive", time_to_live, None),
        )

    def with_deprecated(self, *, reason: str | None = None) -> Self:
        return self._with("withDeprecated", Arg("reason", reason, None))

    def with_check(self) -> Self:
        return self._with("withCheck")

    def with_generator(self) -> Self:
        return self._with("withGenerator")

    def with_up(self) -> Self:
        return self._with("withUp")

    def with_agent(self) -> Self:
        return self._with("withAgent")

    def with_arg(  # noqa: PLR0913
        self,
        name: str,
        type_def: TypeDef,
        *,
        description: str | None = "",
        default_value: str | None = None,
        default_path: str | None = "",
        default_address: str | None = "",
        ignore: Sequence[str] | None = None,
        deprecated: str | None = None,
    ) -> Self:
        return self._with(
            "withArg",
            Arg("name", name),
            Arg("typeDef", type_def),
            Arg("description", description, ""),
            Arg("defaultValue", default_value, None),
            Arg("defaultPath", default_path, ""),
            Arg("ignore", [] if ignore is None else list(ignore), []),
            Arg("deprecated", deprecated, None),
            Arg("defaultAddress", default_address, ""),
        )


class Module(_Object):
    def with_description(self, description: str) -> Self:
        return self._with("withDescription", Arg("description", description))

    def with_object(self, object_: TypeDef) -> Self:
        return self._with("withObject", Arg("object", object_))

    def with_interface(self, iface: TypeDef) -> Self:
        return self._with("withInterface", Arg("iface", iface))

    def with_enum(self, enum: TypeDef) -> Self:
        return self._with("withEnum", Arg("enum", enum))


class Error(_Object):
    def with_value(self, name: str, value: str) -> Self:
        """Attach a value, as JSON text."""
        return self._with("withValue", Arg("name", name), Arg("value", value))


@dataclasses.dataclass(slots=True)
class ArgValue:
    name: str
    # JSON text.
    value: str


class FunctionCall(Type):
    async def parent_name(self) -> str:
        return await self._select("parentName", []).execute(str)

    async def name(self) -> str:
        return await self._select("name", []).execute(str)

    async def parent(self) -> str:
        """The parent object's state, as JSON text."""
        return await self._select("parent", []).execute(str)

    async def input_args(self) -> list[ArgValue]:
        ctx = self._select("inputArgs", []).select_multiple(
            "FunctionCallArgValue",
            name="name",
            value="value",
        )
        return await ctx.execute(list[ArgValue])

    async def return_value(self, value: str) -> None:
        """Set the function's result, as JSON text."""
        await self._select("returnValue", [Arg("value", value)]).execute()

    async def return_error(self, error: Error) -> None:
        await self._select("returnError", [Arg("error", error)]).execute()


def type_def() -> TypeDef:
    return TypeDef(Context().root_select("typeDef", []))


def function(name: str, return_type: TypeDef) -> Function:
    args = [Arg("name", name), Arg("returnType", return_type)]
    return Function(Context().root_select("function", args))


def module() -> Module:
    return Module(Context().root_select("module", []))


def error(message: str) -> Error:
    return Error(Context().root_select("error", [Arg("message", message)]))


def current_function_call() -> FunctionCall:
    return FunctionCall(Context().root_select("currentFunctionCall", []))
