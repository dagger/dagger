import dataclasses
from typing import TypeAlias

from dagger.client import base

PythonName: TypeAlias = str
APIName: TypeAlias = str


@dataclasses.dataclass(slots=True)
class FieldDefinition:
    name: APIName | None
    optional: bool = False


@dataclasses.dataclass(slots=True, frozen=True)
class ObjectDefinition:
    name: PythonName
    doc: str | None = dataclasses.field(default=None, compare=False)


class Enum(base.Enum):
    """A dagger.base.Enum with descriptions for the values.

    Example usage:

    >>> class MyEnum(dagger.mod.Enum):
    >>>     ONE = "ONE", "The first value."
    >>>     TWO = "TWO"  # no description
    """

    __slots__ = ("description",)

    def __new__(cls, value, description=None):
        obj = str.__new__(cls, value)
        obj._value_ = value
        obj.description = description
        return obj
