import dataclasses
from typing import TypeAlias

from dagger.client import base

PythonName: TypeAlias = str
APIName: TypeAlias = str
ContextPath: TypeAlias = str


@dataclasses.dataclass(slots=True)
class FieldDefinition:
    name: APIName | None
    optional: bool = False


@dataclasses.dataclass(slots=True, frozen=True)
class ObjectDefinition:
    name: PythonName
    doc: str | None = dataclasses.field(default=None, compare=False)


class Enum(base.Enum):
    """A string based :py:class:`enum.Enum` with optional descriptions for the values.

    Example usage::

        class Options(dagger.Enum):
            ONE = "ONE", "The first value"
            TWO = "TWO"  # no description
    """

    __slots__ = ("description",)

    def __new__(cls, value, description=None):
        obj = str.__new__(cls, value)
        obj._value_ = value
        obj.description = description
        return obj
