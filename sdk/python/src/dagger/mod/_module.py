# ruff: noqa: BLE001
import contextlib
import dataclasses
import inspect
import json
import logging
import types
import typing
from collections import Counter, defaultdict
from collections.abc import Callable, MutableMapping
from typing import Any, TypeAlias, TypeVar

import anyio
import cattrs
from rich.console import Console
from typing_extensions import dataclass_transform, overload

import dagger
from dagger.log import configure_logging

from ._converter import make_converter
from ._exceptions import (
    FatalError,
    FunctionError,
    InternalError,
    NameConflictError,
    UserError,
)
from ._resolver import (
    FieldResolver,
    Func,
    FunctionResolver,
    Resolver,
)
from ._types import APIName, FieldDefinition, ObjectDefinition
from ._utils import asyncify, get_doc, to_pascal_case, transform_error

errors = Console(stderr=True, style="bold red")
logger = logging.getLogger(__name__)

FIELD_DEF_KEY = "dagger_field"

T = TypeVar("T", bound=type)

ObjectName: TypeAlias = str
ResolverName: TypeAlias = str

ObjectResolvers: TypeAlias = MutableMapping[ResolverName, Resolver]
Resolvers: TypeAlias = MutableMapping[ObjectDefinition, ObjectResolvers]


class Module:
    """Builder for a :py:class:`dagger.Module`.

    Arguments
    ---------
    log_level:
        Configure logging with this minimal level. If `None`, logging
        is not configured.
    """

    # TODO: Hook debug from `--debug` flag in CLI?
    # TODO: Default logging to logging.WARNING before release.
    def __init__(self, *, log_level: int | str | None = logging.DEBUG):
        self._log_level = log_level
        self._converter: cattrs.Converter = make_converter()
        self._resolvers: list[Resolver] = []
        self._fn_call = dagger.current_function_call()
        self._mod = dagger.current_module()

    def add_resolver(self, resolver: Resolver):
        self._resolvers.append(resolver)

    def get_resolvers(self, mod_name: str) -> Resolvers:  # noqa: C901
        grouped: Resolvers = defaultdict(dict)

        # Convenience for having top-level functions be registered in
        # a main object (object named after the module) implicitly.
        main_object = ObjectDefinition(to_pascal_case(mod_name))

        # This is to validate if every object name corresponds to a different origin.
        object_names: dict[ObjectName, set[type | None]] = defaultdict(set)

        # This is to validate if an object doesn't have duplicate resolver names.
        resolver_names: dict[tuple[ObjectName, ResolverName], int] = Counter()

        for resolver in self._resolvers:
            obj_def: ObjectDefinition

            if resolver.origin is None:
                if isinstance(resolver, FunctionResolver):
                    func: types.FunctionType = resolver.wrapped_func
                    qualname = func.__qualname__.split("<locals>.", 1)[-1]
                    if "." in qualname:
                        msg = (
                            f"Function “{func.__qualname__}” seems to be defined "
                            "in a class that's not decorated with @object_type."
                        )
                        raise UserError(msg)

                if isinstance(resolver, FieldResolver):
                    msg = (
                        f"Field “{resolver.original_name}” seems to be defined "
                        "without a @object_type decorated class."
                    )
                    raise UserError(msg)

                obj_def = main_object
            else:
                if not inspect.isclass(resolver.origin):
                    msg = (
                        f"Unexpected non-class origin for “{resolver.original_name}”: "
                        f" {resolver.origin!r}"
                    )
                    raise UserError(msg)

                if not hasattr(resolver.origin, "__dagger_type__"):
                    msg = f"Class “{resolver.origin.__name__}” is missing @object_type."
                    raise UserError(msg)

                obj_def = resolver.origin.__dagger_type__  # type: ignore generalTypeIssues

            object_names[obj_def.name].add(resolver.origin)
            resolver_names[(obj_def.name, resolver.name)] += 1
            grouped[obj_def][resolver.name] = resolver

        if main_object not in grouped:
            msg = (
                f"Module “{mod_name}” doesn't define any top-level functions or "
                f"a “{main_object.name}” class decorated with @object_type."
            )
            raise UserError(msg)

        with contextlib.suppress(StopIteration):
            name = next(n for n, s in object_names.items() if len(s) > 1)
            msg = f"Object “{name}” is defined multiple times."
            if name == main_object.name:
                msg = (
                    f"{msg} Either define top-level functions or as methods "
                    f"of a class named “{name}” but not both."
                )
            raise NameConflictError(msg)

        if resolver_names.total() != len(resolver_names):
            (pn, rn), c = resolver_names.most_common(1)[0]
            msg = f"Resolver “{pn}.{rn}” is defined {c} times."
            raise NameConflictError(msg)

        return grouped

    def get_resolver(
        self,
        resolvers: Resolvers,
        parent_name: str,
        name: str,
    ) -> Resolver:
        try:
            resolver = resolvers[ObjectDefinition(parent_name)][name]
        except KeyError as e:
            msg = f"Unable to find resolver: {parent_name}.{name}"
            raise FatalError(msg) from e

        logger.debug("resolver => %s.%s", parent_name, name)

        return resolver

    def __call__(self) -> None:
        if self._log_level is not None:
            configure_logging(self._log_level)
        anyio.run(self._run)

    async def _run(self):
        async with await dagger.connect():
            await self._serve()

    async def _serve(self):
        mod_name = await self._mod.name()
        parent_name = await self._fn_call.parent_name()
        resolvers = self.get_resolvers(mod_name)

        result = (
            await self._invoke(resolvers, parent_name)
            if parent_name
            else await self._register(resolvers)
        )

        try:
            output = json.dumps(result)
        except (TypeError, ValueError) as e:
            msg = f"Failed to serialize result: {e}"
            raise InternalError(msg) from e

        logger.debug("output => %s", repr(output))
        await self._fn_call.return_value(dagger.JSON(output))

    async def _register(self, resolvers: Resolvers) -> dagger.ModuleID:
        # Resolvers are collected at import time, but only actually
        # registered during "serve".
        mod = self._mod

        for obj, obj_resolvers in resolvers.items():
            typedef = dagger.type_def().with_object(
                obj.name,
                description=obj.doc,
            )
            for r in obj_resolvers.values():
                typedef = r.register(typedef)
                logger.debug("registered => %s.%s", obj.name, r.name)

            mod = mod.with_object(typedef)

        return await mod.id()

    async def _invoke(
        self,
        resolvers: Resolvers,
        parent_name: str,
    ) -> Any:
        name = await self._fn_call.name()
        parent_json = await self._fn_call.parent()
        input_args = await self._fn_call.input_args()

        inputs = {}
        for arg in input_args:
            # NB: These are already loaded by `input_args`,
            # the await just returns the cached value.
            arg_name = await arg.name()
            arg_value = await arg.value()
            try:
                # Cattrs can decode JSON strings but use `json` directly
                # for more granular control over the error.
                inputs[arg_name] = json.loads(arg_value)
            except ValueError as e:
                msg = f"Unable to decode input argument `{arg_name}`: {e}"
                raise InternalError(msg) from e

        resolver = self.get_resolver(resolvers, parent_name, name)
        return await self.get_result(resolver, parent_json, inputs)

    async def get_result(
        self,
        resolver: Resolver,
        parent_json: dagger.JSON,
        inputs: dict[str, Any],
    ) -> Any:
        root = await self.get_root(resolver, parent_json)

        try:
            result = await resolver.get_result(self._converter, root, inputs)
        except Exception as e:
            raise FunctionError(e) from e

        if inspect.iscoroutine(result):
            msg = "Result is a coroutine. Did you forget to add async/await?"
            raise UserError(msg)

        logger.debug("result => %s", repr(result))

        try:
            unstructured = await asyncify(
                self._converter.unstructure,
                result,
                resolver.return_type,
            )
        except Exception as e:
            msg = transform_error(
                e,
                "Failed to unstructure result",
                getattr(root, resolver.original_name, None),
            )
            raise UserError(msg) from e

        logger.debug("unstructured result => %s", repr(unstructured))

        return unstructured

    async def get_root(
        self,
        resolver: Resolver,
        parent_json: dagger.JSON,
    ) -> object | None:
        if parent := parent_json.strip():
            try:
                parent = json.loads(parent)
            except ValueError as e:
                msg = f"Unable to decode parent value `{parent_json}`: {e}"
                raise FatalError(msg) from e

        if not parent:
            return resolver.origin() if resolver.origin else None

        if resolver.origin is None:
            msg = (
                "Unexpected parent value for top-level "
                f"function {resolver.original_name}: {parent_json}"
            )
            raise FatalError(msg)

        return await asyncify(
            self._converter.structure,
            parent,
            resolver.origin,
        )

    def field(
        self,
        *,
        default: Callable[[], Any] | object = ...,
        name: APIName | None = None,
    ) -> Any:
        """Exposes an attribute as a :py:class:`dagger.FieldTypeDef`.

        Should be used in a class decorated with :py:meth:`object_type`.

        Example usage:

        >>> @object_type
        >>> class Foo:
        >>>     bar: str = field(default="foobar")
        >>>     args: list[str] = field(default=list)

        Parameters
        ----------
        default:
            The default value for the field or a 0-argument callable to
            initialize a field's value.
        name:
            An alternative name for the API. Useful to avoid conflicts with
            reserved words.
        """
        field_def = FieldDefinition(name)

        kwargs = {}
        if default is not ...:
            field_def.optional = True
            kwargs["default_factory" if callable(default) else "default"] = default

        return dataclasses.field(
            metadata={FIELD_DEF_KEY: field_def},
            kw_only=True,
            **kwargs,
        )

    @overload
    def function(
        self,
        func: Func,
        *,
        name: None = None,
        doc: None = None,
    ) -> Func:
        ...

    @overload
    def function(
        self,
        *,
        name: APIName | None = None,
        doc: str | None = None,
    ) -> Callable[[Func], Func]:
        ...

    def function(
        self,
        func: Func | None = None,
        *,
        name: APIName | None = None,
        doc: str | None = None,
    ) -> Func | Callable[[Func], Func]:
        """Exposes a Python function as a :py:class:`dagger.Function`.

        Example usage:

        >>> @function
        >>> def foo() -> str:
        >>>     return "bar"

        Parameters
        ----------
        func:
            Should be a top-level function or instance method in a class
            decorated with :py:meth:`object_type`. Can be an async function.
        name:
            An alternative name for the API. Useful to avoid conflicts with
            reserved words.
        doc:
            An alternative description for the API. Useful to use the
            docstring for other purposes.
        """

        def wrapper(func: Func) -> Func:
            if not callable(func):
                msg = f"Expected a callable, got {type(func)}."
                raise UserError(msg)

            resolver = FunctionResolver(
                original_name=func.__name__,
                name=name or func.__name__,
                wrapped_func=func,
                doc=doc or get_doc(func) or "",
                # This will be filled later with @object_type
                # because it's not bound to a class yet.
                origin=None,
            )

            # This is used by `object_type` to find the resolver
            # for updating the origin.
            func.__dagger_resolver__ = resolver

            self.add_resolver(resolver)

            return func

        return wrapper(func) if func else wrapper

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(function, dataclasses.field, dataclasses.Field),
    )
    def object_type(self, cls: T) -> T:
        ...

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(function, dataclasses.field, dataclasses.Field),
    )
    def object_type(self) -> Callable[[T], T]:
        ...

    def object_type(self, cls: T | None = None) -> T | Callable[[T], T]:
        """Exposes a Python class as a :py:class:`dagger.ObjectTypeDef`.

        Used with :py:meth:`field` and :py:meth:`function` to expose
        the object's members.

        Example usage:

        >>> @object_type
        >>> class Foo:
        >>>     @function
        >>>     def bar(self) -> str:
        >>>         return "foobar"
        """

        def wrapper(cls: T) -> T:
            if not inspect.isclass(cls):
                msg = f"Expected a class, got {type(cls)}"
                raise UserError(msg)

            wrapped = dataclasses.dataclass(kw_only=True)(cls)
            return self._process_type(wrapped)

        return wrapper(cls) if cls else wrapper

    def _process_type(self, cls: T) -> T:
        types = typing.get_type_hints(cls)

        for field in dataclasses.fields(cls):
            field_def: FieldDefinition | None
            if field_def := field.metadata.get(FIELD_DEF_KEY, None):
                r = FieldResolver(
                    name=field_def.name or field.name,
                    original_name=field.name,
                    doc=get_doc(field.type),
                    type_annotation=types[field.name],
                    is_optional=field_def.optional,
                    origin=cls,
                )
                self.add_resolver(r)

        for _, member in inspect.getmembers(cls):
            resolver: FunctionResolver | None
            if resolver := getattr(member, "__dagger_resolver__", None):
                resolver.origin = cls

        cls.__dagger_type__ = ObjectDefinition(  # type: ignore generalTypeIssues
            # Classes should already be in PascalCase, just normalizing here
            # to avoid a mismatch with the module name in PascalCase
            # (for the main object).
            name=to_pascal_case(cls.__name__),
            doc=get_doc(cls),
        )

        return cls
