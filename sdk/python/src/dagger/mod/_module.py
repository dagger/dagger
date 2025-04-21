# ruff: noqa: BLE001
import dataclasses
import enum
import inspect
import json
import logging
import os
import textwrap
import typing
from collections.abc import Awaitable, Callable, Mapping
from typing import Any, TypeVar, cast

import anyio
import anyio.to_thread
import cattrs
import cattrs.gen
from typing_extensions import dataclass_transform, overload

import dagger
from dagger import dag
from dagger.mod._arguments import Parameter
from dagger.mod._converter import make_converter, to_typedef
from dagger.mod._exceptions import (
    ConversionError,
    FatalError,
    FunctionError,
    InternalError,
    UserError,
)
from dagger.mod._resolver import (
    Constructor,
    Field,
    Func,
    Function,
    ObjectType,
    P,
    R,
)
from dagger.mod._types import APIName, FieldDefinition, FunctionDefinition, PythonName
from dagger.mod._utils import (
    asyncify,
    await_maybe,
    get_doc,
    get_parent_module_doc,
    is_annotated,
    to_pascal_case,
)

logger = logging.getLogger(__name__)

OBJECT_DEF_KEY: typing.Final[str] = "__dagger_object__"
FIELD_DEF_KEY: typing.Final[str] = "__dagger_field__"
FUNCTION_DEF_KEY: typing.Final[str] = "__dagger_function__"

T = TypeVar("T", bound=type)


class Module:
    """Builder for a :py:class:`dagger.Module`."""

    def __init__(self, name: str = os.getenv("DAGGER_MODULE_NAME", "")):
        self.name: str = name
        self._converter: cattrs.Converter = make_converter()
        self._objects: dict[str, ObjectType] = {}
        self._enums: dict[str, type[enum.Enum]] = {}
        self._main: ObjectType | None = None

    @property
    def main_cls(self) -> type:
        assert self._main is not None
        return self._main.cls

    def is_main(self, other: ObjectType) -> bool:
        """Check if the given object is the main object of the module."""
        return self.main_cls is other.cls

    def set_module_name(self, name: str):
        self.name = name
        self._main = self.get_object(to_pascal_case(name))

    def __call__(self) -> None:
        anyio.run(self._run)

    async def _run(self):
        async with await dagger.connect():
            await self.serve()

    async def serve(self):
        self.set_module_name(await dag.current_module().name())

        try:
            if parent_name := await dag.current_function_call().parent_name():
                result = await self._invoke(parent_name)
            else:
                result = await self._register()
        except FunctionError as e:
            logger.exception("Error while executing function")
            await dag.current_function_call().return_error(dag.error(str(e)))
            raise SystemExit(2) from None

        try:
            output = json.dumps(result)
        except (TypeError, ValueError) as e:
            msg = f"Failed to serialize result: {e}"
            raise InternalError(msg) from e

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(
                "output => %s",
                textwrap.shorten(repr(output), 144),
            )

        await dag.current_function_call().return_value(dagger.JSON(output))

    async def _register(self) -> dagger.ModuleID:  # noqa: C901, PLR0912
        """Register the module and its types with the Dagger API."""
        mod = dag.module()

        # Object types
        for obj_name, obj_type in self._objects.items():
            if self.is_main(obj_type):
                # Only the main object's constructor is needed.
                # It's the entrypoint to the module.
                obj_type.add_constructor()

                # Module description from main object's parent module
                if desc := get_parent_module_doc(obj_type.cls):
                    mod = mod.with_description(desc)

            # Object/interface type
            type_def = dag.type_def()
            if obj_type.interface:
                type_def = type_def.with_interface(
                    obj_name,
                    description=get_doc(obj_type.cls),
                )
            else:
                type_def = type_def.with_object(
                    obj_name,
                    description=get_doc(obj_type.cls),
                )

            # Object fields
            if obj_type.fields:
                types = typing.get_type_hints(obj_type.cls)

                for field_name, field in obj_type.fields.items():
                    type_def = type_def.with_field(
                        field_name,
                        to_typedef(types[field.original_name]),
                        description=get_doc(field.return_type),
                    )

            # Object/interface functions
            for func_name, func in obj_type.functions.items():
                func_def = dag.function(func_name, to_typedef(func.return_type))

                if doc := func.doc:
                    func_def = func_def.with_description(doc)

                for param in func.parameters.values():
                    arg_def = to_typedef(param.resolved_type)

                    if param.is_nullable:
                        arg_def = arg_def.with_optional(True)

                    func_def = func_def.with_arg(
                        param.name,
                        arg_def,
                        description=param.doc,
                        default_value=param.default_value,
                        default_path=param.default_path,
                        ignore=param.ignore,
                    )

                type_def = (
                    type_def.with_constructor(func_def)
                    if func_name == ""
                    else type_def.with_function(func_def)
                )

            # Add object/interface to module
            if obj_type.interface:
                mod = mod.with_interface(type_def)
            else:
                mod = mod.with_object(type_def)

        # Enum types
        for name, cls in self._enums.items():
            enum_def = dag.type_def().with_enum(name, description=get_doc(cls))
            for member in cls:
                enum_def = enum_def.with_enum_value(
                    str(member.value),
                    description=getattr(member, "description", None),
                )
            mod = mod.with_enum(enum_def)

        return await mod.id()

    async def _invoke(self, parent_name: str) -> Any:
        """Invoke a function and return its result.

        This includes getting the call context from the API and deserializing data.
        """
        fn_call = dag.current_function_call()
        name = await fn_call.name()
        parent_json = await fn_call.parent()
        input_args = await fn_call.input_args()

        parent_state: dict[str, Any] = {}
        if parent_json.strip():
            try:
                parent_state = json.loads(parent_json) or {}
            except ValueError as e:
                msg = f"Unable to decode parent value `{parent_json}`: {e}"
                raise FatalError(msg) from e

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

    async def get_structured_result(
        self,
        parent_name: str,
        parent_state: Mapping[str, Any],
        name: str,
        raw_inputs: Mapping[str, Any],
    ):
        """Execute a function and return its result as a primitive value."""
        obj_type = self.get_object(parent_name)

        if name == "":
            fn = obj_type.get_constructor()
        else:
            parent = await self._get_parent_instance(obj_type.cls, parent_state)

            # NB: fields are not executed by the SDK, they're returned directly by
            # the engine, but this is still useful for testing.
            if name in obj_type.fields:
                f = obj_type.fields[name]
                result = getattr(parent, f.original_name)
                return result, f.return_type

            fn = obj_type.get_bound_function(parent, name)

        inputs = await self._convert_inputs(fn.parameters, raw_inputs)
        bound = fn.bind_arguments(**inputs)

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug("func => %s", repr(fn.signature))
            logger.debug("input args => %s", repr(raw_inputs))
            logger.debug("bound args => %s", repr(bound.arguments))

        try:
            result = await self.call(fn.wrapped, *bound.args, **bound.kwargs)
        except Exception as e:
            raise FunctionError(e) from e

        if inspect.iscoroutine(result):
            msg = "Result is a coroutine. Did you forget to add async/await?"
            raise UserError(msg)

        return result, fn.return_type

    async def get_result(
        self,
        parent_name: str,
        parent_state: Mapping[str, Any],
        name: str,
        raw_inputs: Mapping[str, Any],
    ) -> Any:
        result, return_type = await self.get_structured_result(
            parent_name,
            parent_state,
            name,
            raw_inputs,
        )
        if return_type is not None:
            return await self.unstructure(result, return_type)
        return None

    async def call(self, func: Func[P, R], *args: P.args, **kwargs: P.kwargs) -> R:
        """Call a function and return its result."""
        return await await_maybe(func(*args, **kwargs))

    async def structure(self, obj: Any, cl: type[T], origin: Any | None = None) -> T:
        """Convert a primitive value to the expected type."""
        try:
            return await asyncify(self._converter.structure, obj, cl)
        except Exception as e:
            raise ConversionError(e, origin=origin) from e

    async def unstructure(self, obj: Any, unstructure_as: Any) -> Awaitable[Any]:
        """Convert a result to primitive values."""
        try:
            return await asyncify(self._converter.unstructure, obj, unstructure_as)
        except Exception as e:
            msg = "Failed to convert result to primitive values"
            raise ConversionError(e).as_user(msg) from e

    def get_object(self, name: str) -> ObjectType:
        """Get the object type definition for the given name."""
        try:
            return self._objects[name]
        except KeyError as e:
            msg = f"No `@dagger.object_type` decorated class named {name} was found"
            raise UserError(msg) from e

    async def _get_parent_instance(self, cls: type[T], state: Mapping[str, Any]) -> T:
        """Instantiate the parent object from its state."""
        try:
            return await self.structure(state, cls)
        except ConversionError as e:
            msg = f"Failed to instantiate {cls.__name__}"
            raise e.as_user(msg) from e

    async def _convert_inputs(
        self,
        params: Mapping[PythonName, Parameter],
        inputs: Mapping[APIName, Any],
    ) -> Mapping[PythonName, Any]:
        """Convert arguments from lower level primitives to the expected types."""
        kwargs = {}

        # Convert arguments to the expected type.
        for python_name, param in params.items():
            if param.name not in inputs:
                if not param.is_optional:
                    msg = f"Missing required argument: {python_name}"
                    raise UserError(msg)

                if param.has_default:
                    continue

            # If the argument is optional and has no default, it's a nullable type.
            # According to GraphQL spec, null is a valid value in case it's omitted.
            value = inputs.get(param.name)
            type_ = param.resolved_type

            try:
                kwargs[python_name] = await self.structure(value, type_)
            except ConversionError as e:
                msg = f"Invalid argument `{param.name}`"
                raise e.as_user(msg) from e

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug("structured args => %s", repr(kwargs))

        return kwargs

    def field(
        self,
        *,
        default: Callable[[], Any] | object = ...,
        name: APIName | None = None,
        init: bool = True,
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
        """
        kwargs = {}
        optional = False

        if default is not ...:
            optional = True
            kwargs["default_factory" if callable(default) else "default"] = default

        return dataclasses.field(
            metadata={FIELD_DEF_KEY: FieldDefinition(name, optional)},
            kw_only=True,
            init=init,
            repr=init,  # default repr shows field as an __init__ argument
            **kwargs,
        )

    @overload
    def function(
        self,
        func: Func[P, R],
        *,
        name: APIName | None = None,
        doc: str | None = None,
    ) -> Func[P, R]: ...

    @overload
    def function(
        self,
        *,
        name: APIName | None = None,
        doc: str | None = None,
    ) -> Callable[[Func[P, R]], Func[P, R]]: ...

    def function(
        self,
        func: Func[P, R] | None = None,
        *,
        name: APIName | None = None,
        doc: str | None = None,
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
        """

        # TODO: Wrap appropriately
        def wrapper(func: Func[P, R]) -> Func[P, R]:
            # TODO: Use beartype to validate
            assert callable(func), f"Expected a callable, got {type(func)}."

            meta = FunctionDefinition(name, doc)

            if inspect.isclass(func):
                return Constructor(func, meta)

            setattr(func, FUNCTION_DEF_KEY, meta)

            return func

        return wrapper(func) if func else wrapper

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(function, dataclasses.field, dataclasses.Field),
    )
    def object_type(self, cls: T) -> T: ...

    @overload
    @dataclass_transform(
        kw_only_default=True,
        field_specifiers=(function, dataclasses.field, dataclasses.Field),
    )
    def object_type(self) -> Callable[[T], T]: ...

    def object_type(self, cls: T | None = None) -> T | Callable[[T], T]:
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
        """

        def wrapper(cls: T) -> T:
            if not inspect.isclass(cls):
                msg = f"Expected a class, got {type(cls)}"
                raise UserError(msg)

            # Check for InitVar inside Annotated
            # TODO: Maybe try to transform field automatically, but check
            # with community first on how this is usually handled.
            fields = inspect.get_annotations(cls)
            for name, t in fields.items():
                if is_annotated(t) and isinstance(t.__origin__, dataclasses.InitVar):
                    # Pytohn 3.10 doesn't support `*meta*  syntax
                    # in Annotated[init_t.type, *meta]
                    t.__origin__ = t.__origin__.type
                    msg = (
                        f"Field `{name}` is an InitVar wrapped in Annotated. "
                        f"The correct syntax is: InitVar[{t}]"
                    )
                    raise UserError(msg)

            wrapped = dataclasses.dataclass(kw_only=True)(cls)
            return self._process_type(wrapped)

        return wrapper(cls) if cls else wrapper

    def _process_type(self, cls: T, interface: bool = False) -> T:
        obj_def = ObjectType(cls, interface=interface)

        cls.__dagger_module__ = self
        cls.__dagger_object_type__ = obj_def
        self._objects[cls.__name__] = obj_def

        # Find all constructors from other objects, decorated with `@mod.function`
        def _is_constructor(fn) -> typing.TypeGuard[Constructor]:
            return isinstance(fn, Constructor)

        for _, fn in inspect.getmembers(cls, _is_constructor):
            obj_def.functions[fn.name] = fn

        # Find all methods decorated with `@mod.function`
        def _is_function(fn) -> typing.TypeGuard[Func]:
            return hasattr(fn, FUNCTION_DEF_KEY)

        for _, meth in inspect.getmembers(cls, _is_function):
            fn = Function(meth, getattr(meth, FUNCTION_DEF_KEY))
            fn.origin = cls
            obj_def.functions[fn.name] = fn

        if interface:
            return cls

        # Register hooks for renaming field names in `mod.field()`.
        attr_overrides = {}

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
        """Exposes a Python :py:class:`enum.Enum` as a :py:class:`dagger.EnumTypeDef`.

        The Dagger Python SDK looks for a ``description`` attribute in the enum
        member. There's a convenience base class :py:class:`dagger.Enum` that
        makes it easy to specify those descriptions as a second value.

        Examples
        --------
        Basic usage::

            import enum
            import dagger


            @dagger.enum_type
            class Options(enum.Enum):
                ONE = "ONE"
                TWO = "TWO"


        Using convenience base class for descriptions::

            import dagger


            @dagger.enum_type
            class Options(dagger.Enum):
                ONE = "ONE", "The first value"
                TWO = "TWO", "The second value"


        .. note::
            Only the values and their descriptions are reported to the
            Dagger API. The member's name is only used in Python.
        """

        def wrapper(cls: T) -> T:
            if not inspect.isclass(cls):
                msg = f"Expected an enum, got {type(cls)}"
                raise UserError(msg)

            if not issubclass(cls, enum.Enum):
                msg = f"Class {cls.__name__} is not an enum.Enum"
                raise UserError(msg)

            cls = cast(T, enum.unique(cls))
            self._enums.setdefault(cls.__name__, cls)

            return cls

        return wrapper(cls) if cls else wrapper
