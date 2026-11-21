import dataclasses
import enum
import inspect
import json
import logging
import os
import textwrap
import typing
from collections.abc import Awaitable, Callable, Mapping
from functools import wraps
from typing import Any, TypeVar, cast, dataclass_transform, overload

import anyio
import cattrs
import cattrs.gen
from cattrs.preconf import is_primitive_enum
from cattrs.preconf.json import JsonConverter

from dagger._exceptions import QueryError
from dagger.client._core import configure_converter_enum
from dagger.mod import _api
from dagger.mod._converter import make_converter, to_interface_impl, typedef_from
from dagger.mod._describe import (
    ArgumentDescription,
    EnumDescription,
    EnumMemberDescription,
    FieldDescription,
    FunctionDescription,
    ModuleDescription,
    ObjectDescription,
    describe_type,
)
from dagger.mod._exceptions import (
    BadUsageError,
    FunctionError,
    InvalidInputError,
    InvalidResultError,
    ModuleError,
    ObjectNotFoundError,
    RegistrationError,
    log_exception_only,
    transform_error,
)
from dagger.mod._naming import ModuleNames, module_names
from dagger.mod._resolver import (
    Constructor,
    Field,
    Func,
    Function,
    ObjectType,
    P,
    R,
)
from dagger.mod._types import (
    COLLECTION_BASE_ATTR,
    COLLECTION_BASE_FIELD,
    APIName,
    FieldDefinition,
    FunctionDefinition,
    PythonName,
)
from dagger.mod._utils import (
    asyncify,
    extract_enum_member_doc,
    get_doc,
    get_parent_module_doc,
    is_annotated,
)

logger = logging.getLogger(__package__)

OBJECT_DEF_KEY: typing.Final[str] = "__dagger_object__"
FIELD_DEF_KEY: typing.Final[str] = "__dagger_field__"
FUNCTION_DEF_KEY: typing.Final[str] = "__dagger_function__"
CHECK_DEF_KEY: typing.Final[str] = "__dagger_check__"
GENERATOR_DEF_KEY: typing.Final[str] = "__dagger_generate__"
UP_DEF_KEY: typing.Final[str] = "__dagger_up__"
AGENT_DEF_KEY: typing.Final[str] = "__dagger_agent__"
COLLECTION_DEF_KEY: typing.Final[str] = "__dagger_collection__"
COLLECTION_GET_DEF_KEY: typing.Final[str] = "__dagger_get__"
MODULE_NAME: typing.Final[str] = os.getenv("DAGGER_MODULE", "")
MAIN_OBJECT: typing.Final[str] = os.getenv("DAGGER_MAIN_OBJECT", "")
TYPE_DEF_FILE: typing.Final[str] = os.getenv("DAGGER_MODULE_FILE", "/module.json")

T = TypeVar("T", bound=type)


class Module:
    """Builder for a :py:class:`dagger.Module`."""

    def __init__(self, main_name: str = MAIN_OBJECT):
        self._main_name = main_name
        self._converter: JsonConverter = make_converter()
        self._objects: dict[str, ObjectType] = {}
        self._enums: dict[str, type[enum.Enum]] = {}
        self._main: ObjectType | None = None
        self._names: ModuleNames | None = None
        # Escape hatch if there's too much noise from showing stack traces
        # from exceptions raised in functions by default. Not documented
        # intentionally for now.
        self.log_exceptions = True

    @property
    def main_cls(self) -> type[ObjectType]:
        assert self._main is not None
        return self._main.cls

    @property
    def names(self) -> ModuleNames:
        """The engine's names for the module's interfaces and enums.

        Calls through the module's own interfaces select them. Asked for by
        ``load_names``, before anything is invoked.
        """
        if self._names is None:
            msg = "The module's schema names were not asked for before the call"
            raise ModuleError(msg)
        return self._names

    async def load_names(self) -> None:
        """Ask the engine how it names the module, before anything is invoked.

        A module without interfaces makes no call by the engine's names of its
        own declarations, so it asks nothing.
        """
        if self._names is not None:
            return
        interfaces = [obj for obj in self._objects.values() if obj.interface]
        if not interfaces:
            self._names = ModuleNames()
            return
        self._names = await module_names(interfaces, self._enums.values())
        # Bindings are built with the names they have when first used.
        to_interface_impl.cache_clear()

    def is_own_enum(self, cls: Any) -> bool:
        """Whether a class is one of the enums this module declares."""
        return isinstance(cls, type) and self._enums.get(cls.__name__) is cls

    def is_main(self, other: ObjectType) -> bool:
        """Check if the given object is the main object of the module."""
        return self.main_cls is other.cls

    async def serve(self):
        if await _api.current_function_call().parent_name():
            result = await self.invoke()
        else:
            try:
                result = await self._typedefs()
            except TypeError as e:
                raise RegistrationError(str(e)) from e

        try:
            output = json.dumps(result)
        except (TypeError, ValueError) as e:
            # Not expected to happen because unstructuring should reduce
            # Python complex types to primitive values that are easily
            # serialized to JSON. If not, it's something that should be caught
            # earlier.
            msg = f"Failed to serialize final result as JSON: {e}"
            raise InvalidResultError(msg) from e

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(
                "output => %s",
                textwrap.shorten(repr(output), 144),
            )

        await _api.current_function_call().return_value(output)

    async def register(self):
        """Register the module and its types with the Dagger API."""
        try:
            result = await self._typedefs()
            output = json.dumps(result)
        except TypeError as e:
            raise RegistrationError(str(e), e) from e
        await anyio.Path(TYPE_DEF_FILE).write_text(output)

    async def _typedefs(self) -> str:
        return await _module_from(self.describe()).id()

    def describe(self) -> ModuleDescription:
        """Describe the registered types as plain data."""
        if not self._main_name:
            msg = "Main object name can't be empty"
            raise ValueError(msg)
        try:
            self.get_object(self._main_name)
        except ObjectNotFoundError as e:
            msg = (
                f"Main object with name '{self._main_name}' not found or class not "
                "decorated with '@dagger.object_type'\n"
                f"If you believe the module name '{MODULE_NAME}' is incorrectly "
                "being converted into PascalCase, please file a bug report."
            )
            raise ObjectNotFoundError(msg, extra=e.extra) from None

        description = None
        objects = []
        for obj_name, obj_type in self._objects.items():
            if self.is_main(obj_type):
                # Only the main object's constructor is needed.
                # It's the entrypoint to the module.
                obj_type.get_constructor(self._converter)
                description = get_parent_module_doc(obj_type.cls)
            objects.append(_describe_object(obj_name, obj_type))

        return ModuleDescription(
            main_object=self._main_name,
            description=description,
            objects=tuple(objects),
            enums=tuple(_describe_enum(name, cls) for name, cls in self._enums.items()),
        )

    async def invoke(self) -> str:
        """Invoke a function and return its result.

        This includes getting the call context from the API and deserializing data.
        """
        fn_call = _api.current_function_call()
        parent_name = await fn_call.parent_name()

        if not parent_name:
            msg = (
                "Seems like the SDK module isn't registering the types correctly. "
                "This is a bug."
            )
            raise RegistrationError(msg)

        name = await fn_call.name()
        parent_json = await fn_call.parent()
        input_args = await fn_call.input_args()

        parent_state: dict[str, Any] = {}
        if parent_json.strip():
            try:
                parent_state = json.loads(parent_json) or {}
            except ValueError as e:
                logger.exception("Failed to decode JSON parent value")
                msg = "Unable to decode the parent object's state"
                extra = {
                    "parent_json": parent_json,
                }
                raise InvalidInputError(msg, extra=extra) from e

        inputs = {}
        for arg in input_args:
            try:
                # Cattrs can decode JSON strings but use `json` directly
                # for more granular control over the error.
                inputs[arg.name] = json.loads(arg.value)
            except ValueError as e:
                logger.exception("Failed to decode JSON input value")
                msg = f"Unable to decode input argument '{arg.name}'"
                extra = {
                    "json_value": arg.value,
                }
                raise InvalidInputError(msg, extra=extra) from e

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(
                "invoke => %s",
                {
                    "parent_name": parent_name,
                    "parent_json": textwrap.shorten(parent_json, 144),
                    "name": name,
                    "input_args": textwrap.shorten(repr(inputs), 144),
                },
            )

        await self.load_names()

        result = await self.get_result(
            parent_name,
            parent_state,
            name,
            inputs,
        )

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(
                "result => %s",
                textwrap.shorten(repr(result), 144),
            )

        return result

    async def dispatch(self, request: Mapping[str, Any]) -> Any:
        """Run the call a ModuleEntrypoint forwards.

        The receiver state and the arguments arrive as JSON text.
        """
        receiver = request.get("receiverValue") or ""
        try:
            parent_state = json.loads(receiver) if receiver.strip() else None
            inputs = json.loads(request["fnArgs"])
        except ValueError as e:
            msg = "Unable to decode the call request"
            raise InvalidInputError(msg, extra={"request": request}) from e
        await self.load_names()
        return await self.get_result(
            request["receiverType"],
            parent_state or {},
            request["fnName"],
            inputs,
        )

    async def get_result(
        self,
        parent_name: str,
        parent_state: Mapping[str, Any],
        name: str,
        raw_inputs: Mapping[str, Any],
    ) -> Any:
        """Get function result as an unstructured Python primitive."""
        result, fn = await self.get_structured_result(
            parent_name,
            parent_state,
            name,
            raw_inputs,
        )
        if fn.return_type is not None:
            try:
                return await self.unstructure(result, fn.return_type)
            except Exception as e:
                log_exception_only(e, "Invalid result from function")
                msg = transform_error(
                    e,
                    origin=getattr(fn, "wrapped", None),
                    typ=fn.return_type,
                )
                msg += (
                    "\n"
                    "Please check if the returned value at runtime matches "
                    "the function's declared return type."
                )
                raise InvalidResultError(msg) from e
        return None

    async def get_structured_result(
        self,
        parent_name: str,
        parent_state: Mapping[str, Any],
        name: str,
        raw_inputs: Mapping[str, Any],
    ) -> tuple[Any, Field | Function]:
        """Execute a function and return its result as a primitive value."""
        obj_type = self.get_object(parent_name)

        if name == "":
            fn = obj_type.get_constructor(self._converter)
        else:
            parent = await self._get_parent_instance(obj_type, parent_state)

            # NB: fields are not executed by the SDK, they're returned directly by
            # the engine, but this is still useful for testing.
            if name in obj_type.fields:
                f = obj_type.fields[name]
                result = getattr(parent, f.original_name)
                return result, f

            fn = obj_type.get_bound_function(parent, name)

        inputs = await self._convert_inputs(fn, raw_inputs)
        bound = fn.bind_arguments(**inputs)

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug("func => %s", repr(fn.signature))
            logger.debug("input args => %s", repr(raw_inputs))
            logger.debug("bound args => %s", repr(bound.arguments))

        result = await self.call(fn.wrapped, *bound.args, **bound.kwargs)

        # Provide better errors for missing async/await
        if inspect.iscoroutine(result):
            result.close()  # avoid RuntimeWarning

            if not inspect.iscoroutinefunction(fn.wrapped):
                msg = (
                    f"Function '{fn}' returned a coroutine.\n"
                    "Did you forget to add 'async' to the function signature?"
                )
            else:
                msg = (
                    f"Async function '{fn}' was never awaited.\n"
                    "Did you forget to add an 'await' to the return value?"
                )
            raise FunctionError(msg) from None

        return result, fn

    async def call(self, func: Func[P, R], *args: P.args, **kwargs: P.kwargs) -> R:
        """Call a function and return its result."""
        try:
            # We could await based on the return value instead of checking function
            # color but that would silently allow incorrect code which is
            # especially bad if not intentional and we don't warn user about it.
            result = func(*args, **kwargs)
            if inspect.iscoroutinefunction(func):
                result = await cast(typing.Awaitable[R], result)
        except FunctionError:
            # Escape hatch to fully control logging from user code.
            raise
        except QueryError as e:
            tb = e.__traceback__
            # Exclude the line in "try" above
            if tb:
                tb = tb.tb_next
            # Exclude the underlying TransportQueryError to reduce noise
            e.__cause__ = None
            logger.exception(
                "API error while executing function",
                exc_info=(type(e), e, tb),
            )
            # Preserve API error so it's properly propagated.
            raise e from None
        except Exception as e:
            # Escape hatch if too noisy.
            if self.log_exceptions:
                # Logging the exception will show the full stack trace on stderr.
                logger.exception("Unhandled exception while executing function")
            raise FunctionError(str(e)) from e

        return result

    async def structure(self, obj: Any, cl: type[T]) -> T:
        """Convert a primitive value to the expected type."""
        return await asyncify(self._converter.structure, obj, cl)

    async def unstructure(self, obj: Any, unstructure_as: Any) -> Awaitable[Any]:
        """Convert a result to primitive values."""
        return await asyncify(self._converter.unstructure, obj, unstructure_as)

    def get_object(self, name: str) -> ObjectType:
        """Get the object type definition for the given name."""
        try:
            return self._objects[name]
        except KeyError:
            # Not expected to happen during invoke because registration should
            # fail first.
            msg = f"No '@dagger.object_type' decorated class named '{name}' was found"
            extra = {"objects_found": self._objects.keys()}
            raise ObjectNotFoundError(msg, extra=extra) from None

    async def _get_parent_instance(
        self,
        obj_type: ObjectType[T],
        state: Mapping[str, Any],
    ) -> T:
        """Instantiate the parent object from its state."""
        try:
            return await self.structure(state, obj_type.cls)
        except Exception as e:
            log_exception_only(e, "Failed to instantiate parent object")
            msg = transform_error(
                e,
                f"Failed to instantiate parent object '{obj_type}'",
                origin=obj_type.cls,
                typ=obj_type.cls,
            )
            # If API is able to make the call this is likely a bug in the SDK.
            # For example, if the registration phase reports a type that isn't
            # compatible with cattrs' converter.
            msg += (
                "\n"
                "This could be an error in the Python SDK. "
                "If so, please file a bug report."
            )
            extra = {"object_state": state}
            raise InvalidInputError(msg, extra=extra) from e

    async def _convert_inputs(
        self,
        fn: Function,
        inputs: Mapping[APIName, Any],
    ) -> Mapping[PythonName, Any]:
        """Convert arguments from lower level primitives to the expected types."""
        kwargs = {}

        # Convert arguments to the expected type.
        for python_name, param in fn.parameters.items():
            if param.name not in inputs:
                if not param.is_optional:
                    msg = f"Missing required function argument '{python_name}'"
                    raise InvalidInputError(msg)

                if param.has_default:
                    continue

            # If the argument is optional and has no default, it's a nullable type.
            # According to GraphQL spec, null is a valid value in case it's omitted.
            value = inputs.get(param.name)
            type_ = param.resolved_type

            try:
                kwargs[python_name] = await self.structure(value, type_)
            except Exception as e:
                log_exception_only(
                    e,
                    "Failed to convert from primitive input value for argument '%s'",
                    param.name,
                )
                msg = transform_error(
                    e,
                    (
                        "Failed to convert from primitive input value for argument "
                        f"'{param.name}'"
                    ),
                    origin=fn.wrapped,
                    typ=type_,
                )
                # Same as before, the API can't reasonably hold a value that
                # contradicts its type.
                msg += (
                    "\n"
                    "This could be an error in the Python SDK. "
                    "If so, please file a bug report."
                )
                extra = {
                    "function_name": fn.original_name,
                    "parameter_name": python_name,
                    "expected_type": type_,
                    "actual_type": type(value),
                }
                raise InvalidInputError(msg, extra=extra) from e

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug("structured args => %s", repr(kwargs))

        return kwargs

    def field(
        self,
        *,
        default: Callable[[], Any] | object = ...,
        name: APIName | None = None,
        init: bool = True,
        deprecated: str | None = None,
    ) -> Any:
        """Exposes an attribute as a :py:class:`dagger.FieldTypeDef`.

        Should be used in a class decorated with :py:meth:`object_type`.

        Example usage::

            @object_type
            class Foo:
                bar: str = field(default="foobar")
                args: list[str] = field(default=list)


        Parameters
        ----------
        default:
            The default value for the field or a 0-argument callable to
            initialize a field's value.
        name:
            An alternative name for the API. Useful to avoid conflicts with
            reserved words.
        init:
            Whether the field should be included in the constructor.
            Defaults to `True`.
        deprecated:
            Optional deprecation message exposed to the engine.
        """
        kwargs = {}
        optional = False

        if default is not ...:
            optional = True
            kwargs["default_factory" if callable(default) else "default"] = default

        return dataclasses.field(
            metadata={FIELD_DEF_KEY: FieldDefinition(name, optional, deprecated)},
            kw_only=True,
            init=init,
            repr=init,  # default repr shows field as an __init__ argument
            **kwargs,
        )

    def collection(self, cls: T) -> T:
        """Mark an exposed object type as a collection."""
        setattr(cls, COLLECTION_DEF_KEY, True)
        obj_def = cls.__dict__.get("__dagger_object_type__")
        if obj_def and COLLECTION_BASE_ATTR not in cls.__dataclass_fields__:
            # @object_type already made the dataclass, without the base.
            cls.__dataclass_fields__[COLLECTION_BASE_ATTR] = _COLLECTION_BASE
            setattr(cls, COLLECTION_BASE_ATTR, None)
            _keep_base_through_init(cls)
            self._process_type(cls, deprecated=obj_def.deprecated)
        return cls

    def get(self, func: Func[P, R]) -> Func[P, R]:
        """Select the exposed item lookup function of a collection."""
        setattr(func, COLLECTION_GET_DEF_KEY, True)
        return func

    def keys(
        self,
        *,
        default: Callable[[], Any] | object = ...,
        name: APIName | None = None,
        init: bool = True,
        deprecated: str | None = None,
    ) -> Any:
        """Expose the stored keys field of a collection."""
        return self._collection_field("keys", default, name, init, deprecated)

    def delta(
        self,
        *,
        default: Callable[[], Any] | object = None,
        name: APIName | None = None,
        init: bool = False,
        deprecated: str | None = None,
    ) -> Any:
        """Expose a field that receives the collection delta."""
        return self._collection_field("delta", default, name, init, deprecated)

    def _collection_field(
        self,
        role: str,
        default: Callable[[], Any] | object,
        name: APIName | None,
        init: bool,
        deprecated: str | None,
    ) -> Any:
        field = self.field(default=default, name=name, init=init, deprecated=deprecated)
        meta = dataclasses.replace(field.metadata[FIELD_DEF_KEY], collection_role=role)
        field.metadata = {**field.metadata, FIELD_DEF_KEY: meta}
        return field

    @overload
    def check(self, func: Func[P, R]) -> Func[P, R]: ...

    @overload
    def check(self) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def check(
        self,
        func: Func[P, R] | None = None,
    ) -> Func[P, R] | Callable[[Func[P, R]], Func[P, R]]:
        """Mark a function as a check.

        Checks are functions that validate conditions and return void/error
        to indicate pass/fail. This decorator can be combined with
        :py:meth:`function`.

        Example usage::

            @object_type
            class MyModule:
                @function
                @check
                def lint(self) -> str:
                    return "All checks passed"

        Parameters
        ----------
        func:
            The function to mark as a check. Should be an instance method in a
            class decorated with :py:meth:`object_type`.
        """

        def wrapper(fn: Func[P, R]) -> Func[P, R]:
            setattr(fn, CHECK_DEF_KEY, True)
            return fn

        return wrapper(func) if func else wrapper

    @overload
    def generate(self, func: Func[P, R]) -> Func[P, R]: ...

    @overload
    def generate(self) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def generate(
        self,
        func: Func[P, R] | None = None,
    ) -> Func[P, R] | Callable[[Func[P, R]], Func[P, R]]:
        """Mark a function as a generator.

        Generators are functions that return a Changeset representing
        changes to be applied. This decorator can be combined with
        :py:meth:`function`.

        Example usage::

            @object_type
            class MyModule:
                @function
                @generate
                def codegen(self) -> dagger.Changeset:
                    # Generate code and return changeset
                    ...

        Parameters
        ----------
        func:
            The function to mark as a generator. Should be an instance method in a
            class decorated with :py:meth:`object_type`.
        """

        def wrapper(fn: Func[P, R]) -> Func[P, R]:
            setattr(fn, GENERATOR_DEF_KEY, True)
            return fn

        return wrapper(func) if func else wrapper

    @overload
    def up(self, func: Func[P, R]) -> Func[P, R]: ...

    @overload
    def up(self) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def up(
        self,
        func: Func[P, R] | None = None,
    ) -> Func[P, R] | Callable[[Func[P, R]], Func[P, R]]:
        """Mark a function as a service for ``dagger up``."""

        def wrapper(fn: Func[P, R]) -> Func[P, R]:
            setattr(fn, UP_DEF_KEY, True)
            return fn

        return wrapper(func) if func else wrapper

    @overload
    def agent(self, func: Func[P, R]) -> Func[P, R]: ...

    @overload
    def agent(self) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def agent(
        self,
        func: Func[P, R] | None = None,
    ) -> Func[P, R] | Callable[[Func[P, R]], Func[P, R]]:
        """Mark a function as an agent middleware.

        Agent middlewares take a base :py:class:`dagger.LLM` and return an
        ``LLM`` with the module's tools and prompting folded onto it. They are
        discovered and composed by ``dagger agent``. Besides the base ``LLM``,
        an agent function may not declare any other required argument. This
        decorator can be combined with :py:meth:`function`.

        Example usage::

            @object_type
            class MyModule:
                @function
                @agent
                def agent(self, base: dagger.LLM) -> dagger.LLM:
                    return base.with_tools(dag.current_node())

        Parameters
        ----------
        func:
            The function to mark as an agent middleware. Should be an instance
            method in a class decorated with :py:meth:`object_type`.
        """

        def wrapper(fn: Func[P, R]) -> Func[P, R]:
            setattr(fn, AGENT_DEF_KEY, True)
            return fn

        return wrapper(func) if func else wrapper

    @overload
    def function(
        self,
        func: Func[P, R],
        *,
        name: APIName | None = None,
        doc: str | None = None,
        deprecated: str | None = None,
    ) -> Func[P, R]: ...

    @overload
    def function(
        self,
        *,
        name: APIName | None = None,
        doc: str | None = None,
        deprecated: str | None = None,
    ) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def function(
        self,
        func: Func[P, R] | None = None,
        *,
        name: APIName | None = None,
        doc: str | None = None,
        cache: str | None = None,
        deprecated: str | None = None,
    ) -> Func[P, R] | Callable[[Func[P, R]], Func[P, R]]:
        """Exposes a Python function as a :py:class:`dagger.Function`.

        Example usage::

            @object_type
            class Foo:
                @function
                def bar(self) -> str:
                    return "foobar"


        Parameters
        ----------
        func:
            Should be an instance method in a class decorated with
            :py:meth:`object_type`. Can be an async function or a class,
            to use it's constructor.
        name:
            An alternative name for the API. Useful to avoid conflicts with
            reserved words.
        doc:
            An alternative description for the API. Useful to use the
            docstring for other purposes.
        deprecated:
            Optional deprecation message exposed to the engine.
        """

        # TODO: Wrap appropriately
        def wrapper(func: Func[P, R]) -> Func[P, R]:
            # TODO: Use beartype to validate
            assert callable(func), f"Expected a callable, got {type(func)}."

            # Check if function is marked as a check or generator
            check = getattr(func, CHECK_DEF_KEY, False)
            generator = getattr(func, GENERATOR_DEF_KEY, False)
            service = getattr(func, UP_DEF_KEY, False)
            agent = getattr(func, AGENT_DEF_KEY, False)

            meta = FunctionDefinition(
                name=name,
                doc=doc,
                cache=cache,
                deprecated=deprecated,
                check=check,
                generator=generator,
                service=service,
                agent=agent,
            )

            if inspect.isclass(func):
                return Constructor(func, meta)

            setattr(func, FUNCTION_DEF_KEY, meta)

            return func

        return wrapper(func) if func else wrapper

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(
            function,
            field,
            keys,
            delta,
            dataclasses.field,
            dataclasses.Field,
        ),
    )
    def object_type(self, cls: T, /, *, deprecated: str | None = None) -> T: ...

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(
            function,
            field,
            keys,
            delta,
            dataclasses.field,
            dataclasses.Field,
        ),
    )
    def object_type(self, *, deprecated: str | None = None) -> Callable[[T], T]: ...

    def object_type(
        self,
        cls: T | None = None,
        *,
        deprecated: str | None = None,
    ) -> T | Callable[[T], T]:
        """Exposes a Python class as a :py:class:`dagger.ObjectTypeDef`.

        Used with :py:meth:`field` and :py:meth:`function` to expose
        the object's members.

        Example usage::

            import dagger


            @dagger.object_type
            class Foo:
                @dagger.function
                def bar(self) -> str:
                    return "foobar"


        Parameters
        ----------
        deprecated:
            Optional deprecation message visible when introspecting the module.
        """

        def wrapper(cls: T) -> T:
            if not inspect.isclass(cls):
                msg = f"Expected a class, got {type(cls)}"
                raise BadUsageError(msg)

            # Check for InitVar inside Annotated
            fields = inspect.get_annotations(cls)
            for name, t in fields.items():
                if is_annotated(t) and isinstance(t.__origin__, dataclasses.InitVar):
                    # Pytohn 3.10 doesn't support `*meta*  syntax
                    # in Annotated[init_t.type, *meta]
                    t.__origin__ = t.__origin__.type
                    msg = (
                        f"Field '{name}' is an InitVar wrapped in Annotated. "
                        f"The correct syntax is: InitVar[{t}]"
                    )
                    raise BadUsageError(msg)

            # A collection keeps opaque engine state as an ordinary private
            # field so copy.copy, deepcopy, and dataclasses.replace all
            # preserve it. @collection adds it if it runs after this.
            if getattr(cls, COLLECTION_DEF_KEY, False):
                cls.__annotations__ = dict(getattr(cls, "__annotations__", {}))
                cls.__annotations__[COLLECTION_BASE_ATTR] = str | None
                setattr(cls, COLLECTION_BASE_ATTR, _collection_base_field())
                _keep_base_through_init(cls)
            wrapped = dataclasses.dataclass(kw_only=True)(cls)
            return self._process_type(wrapped, deprecated=deprecated)

        return wrapper(cls) if cls else wrapper

    def _process_type(
        self,
        cls: T,
        *,
        interface: bool = False,
        deprecated: str | None = None,
    ) -> T:
        obj_def = ObjectType(cls, interface=interface, deprecated=deprecated)

        cls.__dagger_module__ = self
        cls.__dagger_object_type__ = obj_def
        self._objects[cls.__name__] = obj_def
        if cls.__name__ == self._main_name:
            self._main = obj_def

        # Find all constructors from other objects, decorated with `@mod.function`
        def _is_constructor(fn) -> typing.TypeGuard[Constructor]:
            return isinstance(fn, Constructor)

        for _, fn in inspect.getmembers(cls, _is_constructor):
            obj_def.functions[fn.name] = fn

        # Find all methods decorated with `@mod.function`
        def _is_function(fn) -> typing.TypeGuard[Func]:
            return hasattr(fn, FUNCTION_DEF_KEY)

        for _, meth in inspect.getmembers(cls, _is_function):
            fn = Function(
                meth,
                meta=getattr(meth, FUNCTION_DEF_KEY),
                origin=cls,
                converter=self._converter,
            )
            obj_def.functions[fn.name] = fn

        if interface:
            return cls

        # Register hooks for renaming field names in `mod.field()`.
        attr_overrides = {
            COLLECTION_BASE_ATTR: cattrs.gen.override(
                rename=COLLECTION_BASE_FIELD, omit_if_default=True
            )
        }

        # Find all fields exposed with `mod.field()`.
        for field in dataclasses.fields(cls):
            field_def: FieldDefinition | None
            if field_def := field.metadata.get(FIELD_DEF_KEY, None):
                r = Field(
                    meta=field_def,
                    original_name=field.name,
                    return_type=field.type,
                )

                if r.name != r.original_name:
                    attr_overrides[r.original_name] = cattrs.gen.override(rename=r.name)

                obj_def.fields[r.name] = r

        # Include fields that are excluded from the constructor.
        self._converter.register_unstructure_hook(
            cls,
            cattrs.gen.make_dict_unstructure_fn(
                cls,
                self._converter,
                _cattrs_include_init_false=True,
                **attr_overrides,
            ),
        )
        self._converter.register_structure_hook(
            cls,
            cattrs.gen.make_dict_structure_fn(
                cls,
                self._converter,
                _cattrs_include_init_false=True,
                **attr_overrides,
            ),
        )

        return cls

    @overload
    def interface(self, cls: T) -> T: ...

    @overload
    def interface(self) -> Callable[[T], T]: ...

    def interface(self, cls: T | None = None) -> T | Callable[[T], T]:
        """Exposes a Python class as a :py:class:`dagger.InterfaceTypeDef`.

        Used with :py:meth:`function` to expose the interface's functions.

        Example usage::

            import typing
            import dagger


            @dager.interface
            class Foo(typing.Protocol):
                @dagger.function
                async def bar(self) -> str: ...
        """

        def wrapper(cls: T) -> T:
            new_cls = typing.runtime_checkable(cls)
            return self._process_type(new_cls, interface=True)

        return wrapper(cls) if cls else wrapper

    @overload
    def enum_type(self, cls: T) -> T: ...

    @overload
    def enum_type(self) -> Callable[[T], T]: ...

    def enum_type(self, cls: T | None = None) -> T | Callable[[T], T]:
        '''Exposes a Python :py:class:`enum.Enum` as a :py:class:`dagger.EnumTypeDef`.

        Example usage::

            import enum
            import dagger


            @dagger.enum_type
            class Options(enum.Enum):
                """Enumeration description"""

                ONE = "ONE"
                """Description for the first value"""

                TWO = "TWO"
                """Description for the second value"""
        '''

        def wrapper(cls: T) -> T:
            if not inspect.isclass(cls):
                msg = f"Expected an enum.Enum subclass, got {type(cls)}"
                raise BadUsageError(msg)

            if not issubclass(cls, enum.Enum):
                msg = f"Class '{cls.__name__}' is not an enum.Enum subclass"
                raise BadUsageError(msg)

            cls = cast(T, enum.unique(cls))
            self._enums.setdefault(cls.__name__, cls)

            # Primitive enums get converted based on their primitive type rather
            # than the custom hook for converting based on member names so we
            # need to register the hooks for each specific class. Not necessary
            # to add hooks for non-primitive enums because those are already
            # handled by the general enum.Enum subclass check.
            if is_primitive_enum(cls):
                configure_converter_enum(self._converter, cls)

            return cls

        return wrapper(cls) if cls else wrapper


def _collection_base_field() -> Any:
    return dataclasses.field(default=None, repr=False, compare=False)


_COLLECTION_BASE = dataclasses.fields(
    dataclasses.make_dataclass(
        "_CollectionBase",
        [(COLLECTION_BASE_ATTR, str | None, _collection_base_field())],
        kw_only=True,
    )
)[0]


def _keep_base_through_init(cls: type) -> None:
    """Take the base out of the arguments of an __init__ that does not know it."""
    if init := cls.__dict__.get("__init__"):

        @wraps(init)
        def init_with_state(instance, *args, **kwargs):
            base = kwargs.pop(COLLECTION_BASE_ATTR, None)
            init(instance, *args, **kwargs)
            setattr(instance, COLLECTION_BASE_ATTR, base)

        cls.__init__ = init_with_state


def _describe_object(name: str, obj_type: ObjectType) -> ObjectDescription:
    fields: tuple[FieldDescription, ...] = ()
    if obj_type.fields:
        types = typing.get_type_hints(obj_type.cls)
        fields = tuple(
            FieldDescription(
                name=field.name,
                type=describe_type(
                    types[field.original_name],
                    f"type for field '{field.original_name}' in {obj_type}",
                ),
                description=get_doc(field.return_type),
                deprecated=field.meta.deprecated,
                collection_role=field.meta.collection_role,
            )
            for field in obj_type.fields.values()
        )

    functions = []
    constructor = None
    for func_name, func in obj_type.functions.items():
        described = _describe_function(func_name, func, obj_type)
        if func_name == "":
            constructor = described
        else:
            functions.append(described)

    collection = getattr(obj_type.cls, COLLECTION_DEF_KEY, False)
    # A collection without a get function fails to load, and the engine's
    # error names neither the method marked @get nor the missing @function.
    if collection and not any(f.collection_get or f.name == "get" for f in functions):
        for member, value in inspect.getmembers(obj_type.cls):
            if hasattr(value, COLLECTION_GET_DEF_KEY):
                msg = (
                    f"Method '{member}' of '{name}' is marked @get but not "
                    "@function. Add @function: a collection's get function must "
                    "be exposed."
                )
                raise BadUsageError(msg)

    return ObjectDescription(
        name=name,
        interface=obj_type.interface,
        collection=collection,
        description=get_doc(obj_type.cls),
        deprecated=obj_type.deprecated,
        fields=fields,
        functions=tuple(functions),
        constructor=constructor,
    )


def _describe_function(
    name: str,
    func: Function,
    obj_type: ObjectType,
) -> FunctionDescription:
    what = f"function '{name}'" if name else "constructor"
    returns = describe_type(func.return_type, f"return type for {what} in {obj_type}")
    args = tuple(
        ArgumentDescription(
            name=param.name,
            type=describe_type(
                param.resolved_type,
                f"parameter type for '{param.name}' in {what} and {obj_type}",
            ),
            nullable=param.is_nullable,
            description=param.doc,
            default_value=param.default_value,
            default_path=param.default_path,
            default_address=param.default_address,
            ignore=tuple(param.ignore) if param.ignore is not None else None,
            deprecated=param.deprecated,
        )
        for param in func.parameters.values()
    )
    return FunctionDescription(
        name=name,
        returns=returns,
        description=func.doc,
        cache=func.cache_policy,
        deprecated=func.deprecated,
        check=func.check,
        generator=func.generate,
        service=func.service,
        agent=func.agent,
        collection_get=getattr(func.wrapped, COLLECTION_GET_DEF_KEY, False),
        args=args,
    )


def _describe_enum(name: str, cls: type[enum.Enum]) -> EnumDescription:
    member_docs = extract_enum_member_doc(cls)
    members = []
    for member in cls:
        description = getattr(member, "description", None)
        meta = member_docs.get(member.name)
        if description is None and meta and meta.description is not None:
            description = meta.description
        members.append(
            EnumMemberDescription(
                name=member.name,
                value=str(member.value),
                description=description,
                deprecated=meta.deprecated if meta else None,
            )
        )
    return EnumDescription(name, get_doc(cls), tuple(members))


def _module_from(desc: ModuleDescription) -> _api.Module:
    mod = _api.module()
    for obj in desc.objects:
        if obj.name == desc.main_object and desc.description:
            mod = mod.with_description(desc.description)
        type_def = _object_from(obj)
        mod = (
            mod.with_interface(type_def) if obj.interface else mod.with_object(type_def)
        )
    for enum_desc in desc.enums:
        mod = mod.with_enum(_enum_from(enum_desc))
    return mod


def _object_from(obj: ObjectDescription) -> _api.TypeDef:
    type_def = _api.type_def()
    if obj.interface:
        type_def = type_def.with_interface(obj.name, description=obj.description)
    else:
        type_def = type_def.with_object(
            obj.name,
            description=obj.description,
            deprecated=obj.deprecated,
        )
    if obj.collection:
        type_def = type_def.with_collection()
    for field in obj.fields:
        type_def = type_def.with_field(
            field.name,
            typedef_from(field.type),
            description=field.description,
            deprecated=field.deprecated,
        )
        if field.collection_role == "keys":
            type_def = type_def.with_collection_keys(field.name)
        elif field.collection_role == "delta":
            type_def = type_def.with_collection_delta(field.name)
    for func in obj.functions:
        if func.collection_get:
            type_def = type_def.with_collection_get(func.name)
        type_def = type_def.with_function(_function_from(func))
    if obj.constructor is not None:
        type_def = type_def.with_constructor(_function_from(obj.constructor))
    return type_def


def _function_from(func: FunctionDescription) -> _api.Function:  # noqa: C901
    func_def = _api.function(func.name, typedef_from(func.returns))
    if func.description:
        func_def = func_def.with_description(func.description)
    if func.cache == "never":
        func_def = func_def.with_cache_policy("Never")
    elif func.cache == "session":
        func_def = func_def.with_cache_policy("PerSession")
    elif func.cache:
        func_def = func_def.with_cache_policy("Default", time_to_live=func.cache)
    if func.deprecated:
        func_def = func_def.with_deprecated(reason=func.deprecated)
    if func.check:
        func_def = func_def.with_check()
    if func.generator:
        func_def = func_def.with_generator()
    if func.service:
        func_def = func_def.with_up()
    if func.agent:
        func_def = func_def.with_agent()
    for arg in func.args:
        arg_def = typedef_from(arg.type)
        if arg.nullable:
            arg_def = arg_def.with_optional(True)
        func_def = func_def.with_arg(
            arg.name,
            arg_def,
            description=arg.description,
            default_value=arg.default_value,
            default_path=arg.default_path,
            default_address=arg.default_address,
            ignore=list(arg.ignore) if arg.ignore is not None else None,
            deprecated=arg.deprecated,
        )
    return func_def


def _enum_from(enum_desc: EnumDescription) -> _api.TypeDef:
    enum_def = _api.type_def().with_enum(
        enum_desc.name, description=enum_desc.description
    )
    for member in enum_desc.members:
        enum_def = enum_def.with_enum_member(
            member.name,
            value=member.value,
            description=member.description,
            deprecated=member.deprecated,
        )
    return enum_def
