import dataclasses
import inspect

from beartype.door import TypeHint

from ._types import APIName


@dataclasses.dataclass(slots=True, kw_only=True)
class Parameter:
    """Parameter from function signature in :py:class:`FunctionResolver`."""

    name: APIName
    signature: inspect.Parameter
    resolved_type: type
    doc: str | None

    has_default: bool = dataclasses.field(init=False)
    is_optional: bool = dataclasses.field(init=False)

    def __post_init__(self):
        from ._utils import is_nullable

        self.has_default = self.signature.default is not inspect.Signature.empty
        self.is_optional = self.has_default or is_nullable(TypeHint(self.resolved_type))


@dataclasses.dataclass(slots=True, frozen=True)
class Arg:
    """An alternative name when exposing a function argument to the API.

    Useful to avoid conflicts with reserved words.

    Example usage:

    >>> @function
    ... def pull(from_: Annotated[str, Arg("from")]):
    ...     ...
    """

    name: APIName
