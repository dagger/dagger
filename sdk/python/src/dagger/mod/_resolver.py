import dataclasses
import inspect
import json
import logging
import types
from abc import ABC, abstractmethod, abstractproperty
from collections.abc import Callable
from functools import cached_property
from typing import (
    Any,
    Generic,
    TypeVar,
    get_type_hints,
)

import cattrs
from typing_extensions import Self, override

import dagger

from ._arguments import Parameter
from ._converter import to_typedef
from ._exceptions import UserError
from ._types import APIName, PythonName
from ._utils import asyncify, await_maybe, get_arg_name, get_doc, transform_error

logger = logging.getLogger(__name__)


Func = TypeVar("Func", bound=Callable[..., Any])


@dataclasses.dataclass(kw_only=True, slots=True)
class Resolver(ABC):
    original_name: PythonName
    name: APIName = dataclasses.field(repr=False)
    doc: str | None = dataclasses.field(repr=False)
    origin: type | None

    @abstractproperty
    def return_type(self) -> type:
        ...

    @abstractmethod
    async def register(
        self,
        typedef: dagger.TypeDef,
        converter: cattrs.Converter,
    ) -> dagger.TypeDef:
        return typedef

    @abstractmethod
    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object | None,
        inputs: dict[APIName, Any],
    ) -> Any:
        ...


@dataclasses.dataclass(kw_only=True, slots=True)
class FieldResolver(Resolver):
    type_annotation: type
    is_optional: bool

    @override
    async def register(
        self,
        typedef: dagger.TypeDef,
        _: cattrs.Converter,
    ) -> dagger.TypeDef:
        return typedef.with_field(
            self.name,
            to_typedef(self.type_annotation).with_optional(self.is_optional),
            description=self.doc or None,
        )

    @override
    async def get_result(
        self,
        _: cattrs.Converter,
        root: object | None,
        inputs: dict[APIName, Any],
    ) -> Any:
        # NB: This is only useful in unit tests because the API server
        # resolves trivial fields automatically, without invoking the
        # module.
        assert not inputs
        return getattr(root, self.original_name)

    @property
    @override
    def return_type(self):
        """Return the field's type."""
        return self.type_annotation

    def __str__(self):
        assert self.origin is not None
        return f"{self.origin.__name__}.{self.original_name}"


@dataclasses.dataclass(kw_only=True, repr=False)
class FunctionResolver(Resolver, Generic[Func]):
    """Base class for wrapping user-defined functions."""

    wrapped_func: Func

    def __repr__(self):
        return repr(self.wrapped_func)

    @override
    async def register(
        self,
        typedef: dagger.TypeDef,
        converter: cattrs.Converter,
    ) -> dagger.TypeDef:
        """Add a new object to current module."""
        fn = dagger.function(self.name, to_typedef(self.return_type))

        if self.doc:
            fn = fn.with_description(self.doc)

        for param in self.parameters.values():
            fn = fn.with_arg(
                param.name,
                to_typedef(param.resolved_type).with_optional(param.is_optional),
                description=param.doc,
                default_value=await self._get_default_value(param, converter),
            )

        return typedef.with_function(fn) if self.name else typedef.with_constructor(fn)

    async def _get_default_value(
        self,
        param: Parameter,
        converter: cattrs.Converter,
    ) -> dagger.JSON | None:
        if not param.is_optional:
            return None

        default_value = param.signature.default

        if (
            dataclasses.is_dataclass(self.wrapped_func)
            and repr(default_value) == "<factory>"
        ):
            return None
            field = next(
                f for f in dataclasses.fields(self.wrapped_func) if f.name == param.name
            )

            if not field or field.default_factory is dataclasses.MISSING:
                return None

            default_value = await asyncify(
                converter.unstructure,
                field.default_factory(),
                param.resolved_type,
            )

        return dagger.JSON(json.dumps(default_value))

    @property
    def return_type(self):
        """Return the resolved return type of the wrapped function."""
        if inspect.isclass(self.wrapped_func) and self.original_name == "__init__":
            return self.wrapped_func
        try:
            r: type = self._type_hints["return"]
        except KeyError:
            return types.NoneType
        if r is Self:
            if self.origin is None:
                msg = "Can't return Self without parent class"
                raise UserError(msg)
            return self.origin
        return r

    @cached_property
    def _type_hints(self):
        return get_type_hints(self.wrapped_func)

    @cached_property
    def signature(self):
        """Return the signature of the wrapped function."""
        return inspect.signature(self.wrapped_func, follow_wrapped=True)

    @cached_property
    def parameters(self):
        """Return the parameter annotations of the wrapped function.

        Keys are the Python parameter names.
        """
        mapping: dict[PythonName, Parameter] = {}

        for param in self.signature.parameters.values():
            if self.origin and param.name == "self":
                continue

            if param.kind is inspect.Parameter.POSITIONAL_ONLY:
                msg = "Positional-only parameters are not supported"
                raise TypeError(msg)

            try:
                # Use type_hints instead of param.annotation to get
                # resolved forward references and stripped Annotated.
                annotation = self._type_hints[param.name]
            except KeyError:
                logger.warning(
                    "Missing type annotation for parameter '%s'",
                    param.name,
                )
                annotation = Any

            parameter = Parameter(
                name=get_arg_name(param.annotation) or param.name,
                signature=param,
                resolved_type=annotation,
                doc=get_doc(param.annotation),
            )

            mapping[param.name] = parameter

        return mapping

    @override
    async def get_result(
        self,
        converter: cattrs.Converter,
        root: object | None,
        inputs: dict[APIName, Any],
    ) -> Any:
        args = (
            (root,)
            if root is not None and not inspect.isclass(self.wrapped_func)
            else ()
        )

        logger.debug("input args => %s", repr(inputs))
        kwargs = await self._convert_inputs(converter, inputs)
        logger.debug("structured args => %s", repr(kwargs))

        try:
            bound = self.signature.bind(*args, **kwargs)
        except TypeError as e:
            msg = f"Unable to bind arguments: {e}"
            raise UserError(msg) from e

        return await await_maybe(self.wrapped_func(*bound.args, **bound.kwargs))

    async def _convert_inputs(
        self,
        converter: cattrs.Converter,
        inputs: dict[APIName, Any],
    ):
        """Convert arguments to the expected parameter types."""
        kwargs: dict[PythonName, Any] = {}

        # Convert arguments to the expected type.
        for python_name, param in self.parameters.items():
            if param.name not in inputs:
                if not param.is_optional:
                    msg = f"Missing required argument: {python_name}"
                    raise UserError(msg)
                continue

            value = inputs[param.name]
            type_ = param.signature.annotation

            try:
                kwargs[python_name] = await asyncify(converter.structure, value, type_)
            except Exception as e:  # noqa: BLE001
                msg = transform_error(
                    e,
                    f"Invalid argument `{param.name}`",
                    self.wrapped_func,
                    type_,
                )
                raise UserError(msg) from e

        return kwargs
