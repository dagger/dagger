import dataclasses
import inspect

from typing_extensions import deprecated

from dagger.mod._types import APIName


@dataclasses.dataclass(slots=True, frozen=True)
class Name:
    """An alternative name when exposing a function argument to the API.

    Useful to avoid conflicts with reserved words.

    Example usage::

        @function
        def pull(from_: Annotated[str, Name("from")]): ...
    """

    name: APIName

    def __str__(self) -> str:
        return self.name


@deprecated("Arg is deprecated, use Name instead.")
class Arg(Name):
    """An alternative name when exposing a function argument to the API.

    .. deprecated::
        Use :py:class:`Name` instead.
    """


@dataclasses.dataclass(slots=True, frozen=True, kw_only=True)
class Parameter:
    """Parameter from function signature in :py:class:`FunctionResolver`."""

    name: APIName

    # Inspect
    signature: inspect.Parameter
    resolved_type: type
    is_nullable: bool

    # Metadata
    doc: str | None

    @property
    def has_default(self) -> bool:
        return self.signature.default is not inspect.Parameter.empty

    @property
    def is_optional(self) -> bool:
        return self.has_default or self.is_nullable
