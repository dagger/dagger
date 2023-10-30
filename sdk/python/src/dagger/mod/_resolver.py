import dataclasses
import inspect
import json
import logging
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
from typing_extensions import override

import dagger

from ._arguments import Parameter
from ._converter import to_typedef
from ._exceptions import FatalError, UserError
from ._types import APIName, MissingType, PythonName
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
    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
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
    def register(self, object_typedef: dagger.TypeDef) -> dagger.TypeDef:
        return object_typedef.with_field(
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
        if inputs:
            msg = f"Unexpected input args for field resolver: {self}"
            raise FatalError(msg)

        if root is None:
            msg = f"Unexpected None root for field resolver: {self}"

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
    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        """Add a new object to current module."""
        fn = dagger.function(self.name, to_typedef(self.return_type))

        if self.doc:
            fn = fn.with_description(self.doc)

        for param in self.parameters.values():
            fn = fn.with_arg(
                param.name,
                to_typedef(param.resolved_type).with_optional(param.is_optional),
                description=param.doc,
                default_value=(
                    dagger.JSON(json.dumps(param.signature.default))
                    if param.is_optional
                    else None
                ),
            )

        return typedef.with_function(fn)

    @property
    def return_type(self):
        """Return the resolved return type of the wrapped function."""
        try:
            return self._type_hints["return"]
        except KeyError:
            return MissingType

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
        args = (root,) if root is not None else ()

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
