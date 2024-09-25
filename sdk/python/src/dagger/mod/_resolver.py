import dataclasses
import inspect
import json
import logging
import types
from collections.abc import Callable, Mapping
from functools import cached_property
from typing import (
    Any,
    Generic,
    ParamSpec,
    Protocol,
    TypeAlias,
    cast,
    get_type_hints,
    overload,
    runtime_checkable,
)

import cattrs
from beartype.door import TypeHint
from graphql.pyutils import camel_to_snake
from typing_extensions import Self, TypeVar

import dagger
from dagger import dag
from dagger.mod._arguments import DefaultPath, Ignore, Parameter
from dagger.mod._converter import to_typedef
from dagger.mod._exceptions import UserError
from dagger.mod._types import APIName, PythonName
from dagger.mod._utils import (
    asyncify,
    await_maybe,
    get_alt_constructor,
    get_alt_name,
    get_doc,
    get_meta,
    is_nullable,
    normalize_name,
    transform_error,
)

logger = logging.getLogger(__name__)

R = TypeVar("R", infer_variance=True)
P = ParamSpec("P")

Func: TypeAlias = Callable[P, R]


@runtime_checkable
class Resolver(Protocol):
    original_name: PythonName
    name: APIName
    doc: str | None
    origin: type | None

    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        """Register the type definition for this resolver."""
        ...

    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object | None,
        inputs: Mapping[APIName, Any],
    ) -> Any:
        """Call resolver and return the result."""
        ...

    @property
    def return_type(self) -> type:
        """Return the resolved return type of the wrapped function."""
        ...


@dataclasses.dataclass(kw_only=True, slots=True)
class FieldResolver:
    original_name: PythonName
    name: APIName = dataclasses.field(repr=False)
    doc: str | None = dataclasses.field(repr=False)

    origin: type | None
    """The class where this field is defined in."""

    type_annotation: type
    is_optional: bool

    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        return typedef.with_field(
            self.name,
            to_typedef(self.return_type),
            description=self.doc or None,
        )

    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object | None,
        inputs: Mapping[APIName, Any],
    ) -> Any:
        # NB: This is only useful in unit tests because the API server
        # resolves trivial fields automatically, without invoking the
        # module.
        assert not inputs
        return getattr(root, self.original_name)

    @property
    def return_type(self) -> type:
        return self.type_annotation

    def __str__(self):
        assert self.origin is not None
        return f"{self.origin.__name__}.{self.original_name}"


# Can't use slots because of @cached_property.
@dataclasses.dataclass(kw_only=True)
class FunctionResolver(Generic[P, R]):
    """Base class for wrapping user-defined functions."""

    original_name: PythonName
    name: APIName = dataclasses.field(repr=False)
    doc: str | None = dataclasses.field(repr=False)

    # NB: If `wrapped_func` is a class, it's only useful to know if the
    # class's constructor is being added to another class.
    origin: type | None
    """Parent class of instance method."""

    wrapped_func: Func[P, R]

    def __str__(self):
        return repr(self.sig_func)

    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        """Add a new object to current module."""
        fn = dag.function(self.name, to_typedef(self.return_type))

        if self.func_doc is not None:
            fn = fn.with_description(self.func_doc)

        for param in self.parameters.values():
            arg_type = to_typedef(param.resolved_type)

            try:
                default = self._serialize_default_value(param)
            except TypeError as e:
                # Rather than failing on a default value that's not JSON
                # serializable and going through hoops to support more and more
                # types, just don't register it. It'll still be registered
                # as optional so the API server will call the function without
                # it and let Python handle it.
                logger.debug(
                    "Not registering default value for %s: %s",
                    param.signature,
                    e,
                )
                default = None
                arg_type = arg_type.with_optional(True)

            fn = fn.with_arg(
                param.name,
                arg_type,
                description=param.doc,
                default_value=default,
                # The engine should validate if these are set on the right types.
                default_path=(
                    param.default_path.from_context if param.default_path else None
                ),
                ignore=param.ignore.patterns if param.ignore else None,
            )

        return typedef.with_function(fn) if self.name else typedef.with_constructor(fn)

    def _serialize_default_value(self, param: Parameter) -> dagger.JSON | None:
        if not param.has_default:
            return None
        return dagger.JSON(json.dumps(param.signature.default))

    @property
    def return_type(self) -> type:
        """Return the resolved return type of the wrapped function."""
        _wrapped_cls = (
            cast(type, self.wrapped_func)
            if inspect.isclass(self.wrapped_func)
            else None
        )

        try:
            r: type = self.type_hints["return"]
        except KeyError:
            # When no return type is specified, assume None.
            return _wrapped_cls or type(None)

        if _wrapped_cls is not None:
            if self.sig_func.__name__ == "__init__":
                if r is not type(None):
                    msg = (
                        "Expected None return type "
                        f"in __init__ constructor, got {r!r}"
                    )
                    raise UserError(msg)
                return _wrapped_cls

            if r not in (Self, self.wrapped_func):
                msg = (
                    f"Expected `{self.wrapped_func.__name__}` return type "
                    f"in {self.sig_func!r}, got {r!r}"
                )
                raise UserError(msg)
            return _wrapped_cls

        if r is Self:
            if self.origin is None:
                msg = "Can't return Self without parent class"
                raise UserError(msg)
            return self.origin

        return r

    @property
    def func(self):
        """Return the callable to invoke."""
        # It should be the same as `wrapped_func` except for the alternative
        # constructor which is different than `wrapped_func`.
        # It's simpler not to execute `__init__` directly since it's unbound.
        return get_alt_constructor(self.wrapped_func) or self.wrapped_func

    @property
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
        is_method = (
            inspect.isclass(self.wrapped_func)
            and self.sig_func.__name__ == "__init__"
            or self.origin
        )
        mapping: dict[PythonName, Parameter] = {}

        for param in self.signature.parameters.values():
            # Skip `self` parameter on instance methods.
            # It will be added manually on `get_result`.
            if is_method and param.name == "self":
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
        p = Parameter(
            name=get_alt_name(param.annotation) or normalize_name(param.name),
            signature=param,
            resolved_type=annotation,
            is_nullable=is_nullable(TypeHint(annotation)),
            doc=get_doc(param.annotation),
            ignore=get_meta(param.annotation, Ignore),
            default_path=get_meta(param.annotation, DefaultPath),
        )

        # These validations are already done by the engine, just repeating them
        # here for better error messages.
        if not p.is_nullable and p.has_default and p.signature.default is None:
            msg = (
                "Can't use a default value of None on a non-nullable type for "
                f"parameter '{param.name}'"
            )
            raise ValueError(msg)

        if p.default_path:
            if p.has_default and not (p.is_nullable and p.signature.default is None):
                msg = (
                    "Can't use DefaultPath with a default value for "
                    f"parameter '{param.name}'"
                )
                raise AssertionError(msg)

            if not p.default_path.from_context:
                # NB: We could instead warn or just ignore, but it's better to fail
                # fast to avoid astonishment.
                msg = (
                    "DefaultPath can't be used with an empty path in "
                    f"parameter '{param.name}'"
                )
                raise ValueError(msg)

        return p

    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object | None,
        inputs: Mapping[APIName, Any],
    ) -> Any:
        # NB: `root` is only needed on instance methods (with first `self` argument).
        # Use bound instance method to remove `self` from the list of arguments.
        func = getattr(root, self.original_name) if root else self.func

        signature = (
            self.signature
            if func is self.sig_func
            else inspect.signature(func, follow_wrapped=True)
        )

        logger.debug("func => %s", repr(signature))
        logger.debug("input args => %s", repr(inputs))
        kwargs = await self._convert_inputs(converter, inputs)
        logger.debug("structured args => %s", repr(kwargs))

        try:
            bound = signature.bind(**kwargs)
        except TypeError as e:
            msg = f"Unable to bind arguments: {e}"
            raise UserError(msg) from e

        return await await_maybe(func(*bound.args, **bound.kwargs))

    async def _convert_inputs(
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
    resolver: Resolver = dataclasses.field(init=False)

    def __post_init__(self):
        original_name = self.func.__name__
        normalized_name = normalize_name(original_name)
        if self.name is None and normalized_name != original_name:
            self.name = normalized_name
        name = original_name if self.name is None else self.name
        origin = None

        if inspect.isclass(self.func):
            if self.name is None:
                name = camel_to_snake(name)
            elif self.name == "":
                origin = self.func

        self.resolver = FunctionResolver(
            original_name=original_name,
            name=name,
            wrapped_func=self.func,
            doc=self.doc,
            origin=origin,
        )

    def __set_name__(self, owner: type, name: str):
        if self.name is None:
            self.name = name
            self.resolver.name = name
        self.resolver.origin = owner

    @overload
    def __get__(self, instance: None, owner: None = None) -> Self: ...

    @overload
    def __get__(self, instance: object, owner: None = None) -> Func[P, R]: ...

    def __get__(self, instance: object | None, owner: None = None) -> Func[P, R] | Self:
        if instance is None:
            return self
        if inspect.isclass(self.func):
            return cast(Func[P, R], self.func)
        return cast(Func[P, R], types.MethodType(self.func, instance))

    def __call__(self, *args: P.args, **kwargs: P.kwargs) -> R:
        # NB: This is only needed for top-level functions because only
        # class attributes can access descriptors via `__get__`. For
        # the top-level functions, you'll get this `Function` instance
        # instead, so we need to proxy the call to the wrapped function.
        return self.func(*args, **kwargs)
