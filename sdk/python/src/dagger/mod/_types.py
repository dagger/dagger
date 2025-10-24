import dataclasses
import warnings
from typing import TypeAlias

from dagger.client import base

PythonName: TypeAlias = str
APIName: TypeAlias = str
ContextPath: TypeAlias = str


@dataclasses.dataclass(slots=True, frozen=True)
class FieldDefinition:
    name: APIName | None
    optional: bool = False


@dataclasses.dataclass(slots=True, frozen=True)
class FunctionDefinition:
    name: APIName | None = None
    doc: str | None = None
    cache: str | None = None


class Enum(str, base.Enum):
    """A string based :py:class:`enum.Enum` with optional descriptions for the values.

    Example usage::

        class Options(dagger.Enum):
            ONE = "ONE", "The first value"
            TWO = "TWO"  # no description

    .. deprecated::
        Use "enum.Enum" instead, with docstrings for descriptions.
    """

    __slots__ = ("description",)

    def __new__(cls, value, description=None):
        warnings.warn(
            (
                "Class 'dagger.Enum' is deprecated: Use 'enum.Enum' instead, "
                "with docstrings for descriptions."
            ),
            DeprecationWarning,
            stacklevel=4,
        )
        obj = str.__new__(cls, value)
        obj._value_ = value
        obj.description = description
        return obj
