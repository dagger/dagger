import dataclasses
import inspect
import logging
import types
from collections.abc import Callable, Mapping
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

import cattrs
from beartype.door import TypeHint
from graphql.pyutils import camel_to_snake
from typing_extensions import Self, TypeVar

from dagger.mod._arguments import Parameter
from dagger.mod._exceptions import UserError
from dagger.mod._types import APIName, PythonName
from dagger.mod._utils import (
    asyncify,
    await_maybe,
    get_alt_constructor,
    get_alt_name,
    get_default_path,
    get_doc,
    get_ignore,
    is_nullable,
    normalize_name,
    transform_error,
)

logger = logging.getLogger(__name__)

R = TypeVar("R", infer_variance=True)
P = ParamSpec("P")

Func: TypeAlias = Callable[P, R]


@dataclasses.dataclass(kw_only=True, slots=True)
class Field:
    original_name: PythonName
    name_override: dataclasses.InitVar[APIName | None]
    name: APIName = dataclasses.field(init=False)
    is_optional: bool
    return_type: Any

    def __post_init__(self, name_override: APIName | None):
        self.name = name_override or normalize_name(self.original_name)


# Can't use slots because of @cached_property.
@dataclasses.dataclass(kw_only=True)
class FunctionResolver(Generic[P, R]):
    """Base class for wrapping user-defined functions."""

    original_name: PythonName
    name: APIName = dataclasses.field(repr=False)
    doc: str | None = dataclasses.field(repr=False)

    wrapped_func: Func[P, R]

    def __str__(self):
        return repr(self.sig_func)

    @property
    def is_constructor(self) -> bool:
        return self.name == ""

    @cached_property
    def return_type(self) -> type:
        """Return the resolved return type of the wrapped function."""
        if inspect.isclass(cls := self.wrapped_func):
            return cls
        try:
            return self.type_hints["return"]
        except KeyError:
            # When no return type is specified, assume None.
            return type(None)

    @property
    def func(self):
        """Return the callable to invoke."""
        # It should be the same as `wrapped_func` except for the alternative
        # constructor which is different than `wrapped_func`.
        # It's simpler not to execute `__init__` directly since it's unbound.
        return get_alt_constructor(self.wrapped_func) or self.wrapped_func

    @cached_property
    def func_doc(self):
        """Return the description for the callable to invoke."""
        return self.doc if self.doc is not None else get_doc(self.func)

    @property
    def sig_func(self):
        """Return the callable to inspect."""
        # For more accurate inspection, as it can be different
        # than the callable to invoke.
        if inspect.isclass(cls := self.wrapped_func):
            return get_alt_constructor(cls) or cls.__init__
        return self.wrapped_func

    @cached_property
    def type_hints(self):
        return get_type_hints(self.sig_func)

    @cached_property
    def signature(self):
        return inspect.signature(self.sig_func, follow_wrapped=True)

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
                raise TypeError(msg)

            mapping[param.name] = self._make_parameter(param)

        return mapping

    def _make_parameter(self, param: inspect.Parameter) -> Parameter:
        """Create a parameter object from an inspect.Parameter."""
        try:
            # Use type_hints instead of param.annotation to get
            # resolved forward references and stripped Annotated.
            annotation = self.type_hints[param.name]
        except KeyError:
            logger.warning(
                "Missing type annotation for parameter '%s'",
                param.name,
            )
            annotation = Any

        if isinstance(annotation, dataclasses.InitVar):
            annotation: Any = annotation.type

        # The Parameter class is just a simple data object. We calculate all
        # the attributes here to avoid cyclic imports from utils, as this is
        # the only place where it needs to be created.
        return Parameter(
            name=get_alt_name(param.annotation) or normalize_name(param.name),
            signature=param,
            resolved_type=annotation,
            is_nullable=is_nullable(TypeHint(annotation)),
            doc=get_doc(param.annotation),
            ignore=get_ignore(param.annotation),
            default_path=get_default_path(param.annotation),
        )

    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object,
        inputs: Mapping[APIName, Any],
    ) -> Any:
        # NB: `root` is only needed on instance methods (with first `self` argument).
        # Use bound instance method to remove `self` from the list of arguments.
        func = (
            self.func
            if inspect.isclass(self.func)
            else getattr(root, self.original_name)
        )

        signature = (
            self.signature
            if func is self.sig_func
            else inspect.signature(func, follow_wrapped=True)
        )

        kwargs = await self.convert_inputs(converter, inputs)

        if logger.isEnabledFor(logging.DEBUG):
            logger.debug("func => %s", repr(signature))
            logger.debug("input args => %s", repr(inputs))
            logger.debug("structured args => %s", repr(kwargs))

        try:
            bound = signature.bind(**kwargs)
        except TypeError as e:
            msg = f"Unable to bind arguments: {e}"
            raise UserError(msg) from e

        return await await_maybe(func(*bound.args, **bound.kwargs))

    async def convert_inputs(
        self,
        converter: cattrs.Converter,
        inputs: Mapping[APIName, Any],
    ):
        """Convert arguments to the expected parameter types."""
        kwargs: dict[PythonName, Any] = {}

        # Convert arguments to the expected type.
        for python_name, param in self.parameters.items():
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
                kwargs[python_name] = await asyncify(converter.structure, value, type_)
            except Exception as e:
                msg = transform_error(
                    e,
                    f"Invalid argument `{param.name}`",
                    self.sig_func,
                    type_,
                )
                raise UserError(msg) from e

        return kwargs


@dataclasses.dataclass(slots=True)
class Function(Generic[P, R]):
    """Descriptor for wrapping user-defined functions."""

    func: Func[P, R]
    name: APIName | None = None
    doc: str | None = None

    @property
    def original_name(self) -> str:
        return self.func.__name__

    @property
    def is_class(self) -> bool:
        return inspect.isclass(self.func)

    def get_resolver(self) -> FunctionResolver[P, R]:
        if (name := self.name) is None:
            name = (
                camel_to_snake(self.original_name)
                if self.is_class
                else normalize_name(self.original_name)
            )
        return FunctionResolver(
            original_name=self.original_name,
            name=name,
            wrapped_func=self.func,
            doc=self.doc,
        )

    def __set_name__(self, _: type, name: str):
        if self.name is None:
            self.name = name

    @overload
    def __get__(self, instance: None, owner: None = None) -> Self: ...

    @overload
    def __get__(self, instance: object, owner: None = None) -> Func[P, R]: ...

    def __get__(self, instance: object | None, owner: None = None) -> Func[P, R] | Self:
        if instance is None:
            return self
        if self.is_class:
            return cast(Func[P, R], self.func)
        return cast(Func[P, R], types.MethodType(self.func, instance))

    def __call__(self, *args: P.args, **kwargs: P.kwargs) -> R:
        return self.func(*args, **kwargs)


@dataclasses.dataclass(slots=True)
class ObjectType:
    cls: type
    fields: dict[APIName, Field] = dataclasses.field(default_factory=dict)
    functions: dict[APIName, FunctionResolver] = dataclasses.field(default_factory=dict)

    def add_constructor(self):
        self.functions[""] = Function(name="", func=self.cls).get_resolver()
