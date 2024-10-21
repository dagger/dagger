# ruff: noqa: BLE001
import dataclasses
import enum
import inspect
import json
import logging
import textwrap
import typing
from collections.abc import Callable, Mapping
from typing import Any, TypeGuard, TypeVar, cast

import anyio
import cattrs
import cattrs.gen
from rich.console import Console
from typing_extensions import dataclass_transform, overload

import dagger
from dagger import dag, telemetry
from dagger.mod._converter import make_converter, to_typedef
from dagger.mod._exceptions import (
    FatalError,
    FunctionError,
    InternalError,
    UserError,
)
from dagger.mod._resolver import (
    Field,
    Func,
    Function,
    ObjectType,
    P,
    R,
)
from dagger.mod._types import APIName, FieldDefinition
from dagger.mod._utils import (
    asyncify,
    get_doc,
    get_parent_module_doc,
    is_annotated,
    to_pascal_case,
    transform_error,
)

errors = Console(stderr=True, style="bold red")
logger = logging.getLogger(__name__)

FIELD_DEF_KEY: typing.Final[str] = "dagger_field"

T = TypeVar("T", bound=type)


class Module:
    """Builder for a :py:class:`dagger.Module`."""

    def __init__(self):
        self._converter: cattrs.Converter = make_converter()
        self._fn_call = dag.current_function_call()
        self._objects: dict[str, ObjectType] = {}
        self._enums: dict[str, type[enum.Enum]] = {}

    def __call__(self) -> None:
        telemetry.initialize()
        anyio.run(self._run)

    async def _run(self):
        async with await dagger.connect():
            await self.serve()

    async def serve(self):
        mod_name = await dag.current_module().name()
        main_obj_name = to_pascal_case(mod_name)

        if main_obj_name not in self._objects:
            msg = (
                f"No `@dagger.object_type` decorated class named {main_obj_name} "
                "was found"
            )
            raise UserError(msg)

        # If parent_name is empty it means we need to register the type definitions.
        if parent_name := await self._fn_call.parent_name():
            result = await self._invoke(parent_name)
        else:
            result = await self._register(main_obj_name)

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

    async def _register(self, main_obj_name: str) -> dagger.ModuleID:  # noqa: C901
        mod = dag.module()

        # Object types
        for obj_name, obj_type in self._objects.items():
            # Main object
            if obj_name == main_obj_name:
                # Module description from main object's parent module
                if desc := get_parent_module_doc(obj_type.cls):
                    mod = mod.with_description(desc)

                # Module constructor is the main object's constructor
                obj_type.add_constructor()

            # Object type
            obj_type_def = dag.type_def().with_object(
                obj_name,
                description=get_doc(obj_type.cls),
            )

            # Object fields
            if obj_type.fields:
                types = typing.get_type_hints(obj_type.cls)

                for field_name, field in obj_type.fields.items():
                    obj_type_def = obj_type_def.with_field(
                        field_name,
                        to_typedef(types[field.original_name]),
                        description=get_doc(field.return_type),
                    )

            # Object functions
            for func_name, func in obj_type.functions.items():
                func_type_def = dag.function(func_name, to_typedef(func.return_type))

                if func_doc := func.func_doc:
                    func_type_def = func_type_def.with_description(func_doc)

                for param in func.parameters.values():
                    arg_type_def = to_typedef(param.resolved_type)

                    if param.is_nullable:
                        arg_type_def = arg_type_def.with_optional(True)

                    func_type_def = func_type_def.with_arg(
                        param.name,
                        arg_type_def,
                        description=param.doc,
                        default_value=param.default_value,
                        default_path=param.default_path,
                        ignore=param.ignore,
                    )

                obj_type_def = (
                    obj_type_def.with_constructor(func_type_def)
                    if func.is_constructor
                    else obj_type_def.with_function(func_type_def)
                )

            # Add object to module
            mod = mod.with_object(obj_type_def)

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
        name = await self._fn_call.name()
        parent_json = await self._fn_call.parent()
        input_args = await self._fn_call.input_args()

        parent_state: dict[str, Any] = {}
        if parent_json.strip():
            try:
                parent_state = json.loads(parent_json)
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

        return await self.get_result(parent_name, parent_state, name, inputs)

    async def get_result(
        self,
        parent_name: str,
        parent_state: Mapping[str, Any],
        name: str,
        inputs: Mapping[str, Any],
    ) -> Any:
        result: Any

        try:
            obj_def = self._objects[parent_name]
        except KeyError as e:
            msg = f"Unable to find parent object '{parent_name}' for function '{name}'"
            raise ValueError(msg) from e

        # Instantiate parent object using class and attributes as primitive values.
        parent = await asyncify(self._converter.structure, parent_state, obj_def.cls)

        if logger.isEnabledFor(logging.DEBUG):
            suffix = f".{name}" if name else "()"
            resolver_str = f"{parent_name}{suffix}"
            logger.debug("resolver => %s", resolver_str)

        # This won't likely get executed because the engine returns the value
        # of fields directly, without calling the SDK. Handled here just in case.
        if fn := obj_def.fields.get(name, None):
            result = getattr(parent, fn.original_name)
        else:
            try:
                fn = obj_def.functions[name]
            except KeyError as e:
                msg = f"Unable to find function '{name}' in object '{parent_name}'"
                raise ValueError(msg) from e
            try:
                result = await fn.get_result(self._converter, parent, inputs)
            except Exception as e:
                raise FunctionError(e) from e

        if inspect.iscoroutine(result):
            msg = "Result is a coroutine. Did you forget to add async/await?"
            raise UserError(msg)

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug(
                "result => %s",
                textwrap.shorten(repr(result), 144),
            )

        try:
            return await asyncify(
                self._converter.unstructure,
                result,
                fn.return_type,
            )
        except Exception as e:
            msg = transform_error(
                e,
                "Failed to unstructure result",
                getattr(obj_def.cls, fn.original_name, None),
            )
            raise UserError(msg) from e

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
        field_def = FieldDefinition(name)

        kwargs = {}
        if default is not ...:
            field_def.optional = True
            kwargs["default_factory" if callable(default) else "default"] = default

        return dataclasses.field(
            metadata={FIELD_DEF_KEY: field_def},
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

        def wrapper(func: Func[P, R]) -> Func[P, R]:
            if not callable(func):
                msg = f"Expected a callable, got {type(func)}."
                raise UserError(msg)

            return Function(func, name, doc)

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

            @object_type
            class Foo:
                @function
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

    def _process_type(self, cls: T) -> T:
        cls.__dagger_module__ = self

        obj_def = self._objects.setdefault(cls.__name__, ObjectType(cls))

        # Register hooks for renaming field names in `mod.field()`.
        attr_overrides = {}

        # Find all fields exposed with `mod.field()`.
        for field in dataclasses.fields(cls):
            field_def: FieldDefinition | None
            if field_def := field.metadata.get(FIELD_DEF_KEY, None):
                r = Field(
                    original_name=field.name,
                    name_override=field_def.name,
                    is_optional=field_def.optional,
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

        # Find all methods decorated with `@mod.function`
        def _is_function(obj: Any) -> TypeGuard[Function]:
            return isinstance(obj, Function)

        for name, fn in inspect.getmembers(cls, _is_function):
            obj_def.functions[name] = fn.get_resolver()

        return cls

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
