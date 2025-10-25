import dataclasses
import inspect
import logging
from collections.abc import Callable
from functools import cached_property
from typing import (
    Any,
    Generic,
    ParamSpec,
    TypeAlias,
    cast,
    get_type_hints,
    overload,
)

from beartype.door import TypeHint
from cattrs.preconf.json import JsonConverter, make_converter
from typing_extensions import Self, TypeVar, override

from dagger.mod._arguments import Parameter
from dagger.mod._exceptions import (
    BadUsageError,
    InvalidInputError,
    RegistrationError,
)
from dagger.mod._types import APIName, FieldDefinition, FunctionDefinition, PythonName
from dagger.mod._utils import (
    get_alt_constructor,
    get_alt_name,
    get_default_path,
    get_doc,
    get_ignore,
    is_nullable,
    is_self,
    list_of,
    normalize_name,
)

logger = logging.getLogger(__package__)

T = TypeVar("T")
R = TypeVar("R", infer_variance=True)
P = ParamSpec("P")

Func: TypeAlias = Callable[P, R]


@dataclasses.dataclass(kw_only=True, slots=True)
class Field:
    meta: FieldDefinition
    original_name: PythonName
    return_type: Any
    name: APIName = dataclasses.field(init=False)

    def __post_init__(self):
        self.name = self.meta.name or normalize_name(self.original_name)


@dataclasses.dataclass
class Function(Generic[P, R]):
    wrapped: Func[P, R]
    meta: FunctionDefinition = dataclasses.field(default_factory=FunctionDefinition)
    original_name: PythonName = dataclasses.field(init=False)
    origin: type | None = dataclasses.field(default=None)
    converter: JsonConverter = dataclasses.field(default_factory=make_converter)

    def __post_init__(self):
        self.original_name = self.wrapped.__name__

    def __str__(self):
        if self.origin is not None:
            return f"{self.origin.__name__}.{self.original_name}"
        return self.original_name

    def __repr__(self):
        return repr(self.wrapped)

    @cached_property
    def name(self):
        return (
            self.meta.name
            if self.meta.name is not None
            else normalize_name(self.original_name)
        )

    @property
    def doc(self):
        """Return the description for the callable to invoke."""
        return self.meta.doc if self.meta.doc is not None else get_doc(self.wrapped)

    @cached_property
    def cache_policy(self):
        return self.meta.cache

    @cached_property
    def type_hints(self):
        return get_type_hints(self.wrapped)

    @cached_property
    def signature(self):
        return inspect.signature(self.wrapped, follow_wrapped=True)

    @cached_property
    def parameters(self):
        """Return the parameter annotations of the wrapped function.

        Keys are the Python parameter names.
        """
        mapping: dict[PythonName, Parameter] = {}

        for param in self.signature.parameters.values():
            # Skip `self` parameter on instance methods.
            # It will be added manually on `get_result`.
            if param.name == "self":
                continue

            if param.kind is inspect.Parameter.POSITIONAL_ONLY:
                msg = "Positional-only parameters are not supported"
                raise BadUsageError(msg)

            mapping[param.name] = self._make_parameter(param)

        return mapping

    def _make_parameter(self, param: inspect.Parameter) -> Parameter:
        """Create a parameter object from an inspect.Parameter."""
        try:
            # Use type_hints instead of param.annotation to get
            # resolved forward references and stripped Annotated.
            annotation = self.type_hints[param.name]
        except KeyError:
            logger.warning("Missing type annotation for parameter '%s'", param.name)
            annotation = Any

        if isinstance(annotation, dataclasses.InitVar):
            annotation: Any = annotation.type

        return Parameter(
            name=get_alt_name(param.annotation) or normalize_name(param.name),
            signature=param,
            resolved_type=annotation,
            is_nullable=is_nullable(TypeHint(annotation)),
            doc=get_doc(param.annotation),
            ignore=get_ignore(param.annotation),
            default_path=get_default_path(param.annotation),
            conv=self.converter,
        )

    @property
    def return_type(self) -> Any:
        """Return the resolved return type of the wrapped function."""
        try:
            r = self.type_hints["return"]
        except KeyError:
            # When no return type is specified, assume None.
            return None

        if self.origin:
            if is_self(r):
                return self.origin

            if (el := list_of(r)) and is_self(el):
                return list[self.origin]

        return r

    def bind_parent(self, parent: object):
        return dataclasses.replace(
            self,
            origin=parent.__class__,
            wrapped=getattr(parent, self.original_name),
        )

    def bind_arguments(self, *args, **kwargs):
        """Bind the function with the given arguments."""
        try:
            bound = self.signature.bind(*args, **kwargs)
            bound.apply_defaults()
        except TypeError as e:
            logger.exception("Unexpected type while binding input values to arguments")
            raise InvalidInputError(str(e)) from e
        return bound


@dataclasses.dataclass(slots=True)
class Constructor(Function[P, R]):
    _wrapped_cls: type[R] = dataclasses.field(init=False)

    def __post_init__(self):
        assert inspect.isclass(self.wrapped)
        self._wrapped_cls = self.wrapped
        self.wrapped = cast(
            Func[P, R],
            get_alt_constructor(self._wrapped_cls) or self._wrapped_cls,
        )

        self.original_name = ""

    def __set_name__(self, _: type, name: str):
        self.original_name = name

    @cached_property
    @override
    def type_hints(self):
        if self.wrapped is self._wrapped_cls:
            # make sure to get type hints for __init__ instead of class
            # because the latter will get it from the dataclass's fields
            # instead of the constructor's arguments.
            return get_type_hints(self._wrapped_cls.__init__)
        return get_type_hints(self.wrapped)

    @override
    def bind_parent(self, parent: object):
        return self

    @overload
    def __get__(self, instance: None, owner: None = None) -> Self: ...

    @overload
    def __get__(self, instance: object, owner: None = None) -> Func[P, R]: ...

    def __get__(self, instance: object | None, owner: None = None) -> Func[P, R] | Self:
        return self if instance is None else self.wrapped

    @property
    @override
    def return_type(self) -> type[R] | type[None]:
        return self._wrapped_cls

    def __call__(self, *args: P.args, **kwargs: P.kwargs) -> R:
        return self.wrapped(*args, **kwargs)


@dataclasses.dataclass(slots=True)
class ObjectType(Generic[T]):
    cls: type[T]
    interface: bool = False
    fields: dict[APIName, Field] = dataclasses.field(default_factory=dict)
    functions: dict[APIName, Function] = dataclasses.field(default_factory=dict)

    def get_constructor(self, conv: JsonConverter | None = None):
        if "" not in self.functions:
            self.functions[""] = Constructor(self.cls)
        if conv is not None:
            self.functions[""].converter = conv
        return self.functions[""]

    def get_bound_function(self, parent: object, name: str) -> Function:
        assert self.cls is parent.__class__
        try:
            fn = self.functions[name]
        except KeyError:
            msg = f"No function '{name}' in {self}"
            raise RegistrationError(msg) from None

        return fn.bind_parent(parent)

    def __str__(self):
        s = "interface" if self.interface else "object"
        return f"{s} '{self.cls.__module__}.{self.cls.__name__}'"
