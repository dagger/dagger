import typing
from collections.abc import Sequence
from typing import Annotated, TypeGuard

from beartype import BeartypeConf, BeartypeViolationVerbosity, beartype
from beartype.door import TypeHint
from beartype.vale import Is, IsInstance, IsSubclass

from .base import Scalar, Type

IDScalar = Annotated[Scalar, Is[lambda obj: type(obj).__name__.endswith("ID")]]


@typing.runtime_checkable
class HasID(typing.Protocol):
    async def id(self) -> IDScalar:
        ...


IDTypeSubclass = Annotated[type[HasID], IsSubclass[Type]]
IDType = Annotated[HasID, IsInstance[Type]]
IDTypeSeq = Annotated[Sequence[IDType], ~IsInstance[str]]

IDTypeSubclassHint = TypeHint(IDTypeSubclass)
IDTypeHint = TypeHint(IDType)
IDTypeSeqHint = TypeHint(IDTypeSeq)


def is_id_type_subclass(v: type) -> TypeGuard[type[Type]]:
    return IDTypeSubclassHint.is_bearable(v)


def is_id_type(v: object) -> TypeGuard[IDType]:
    return IDTypeHint.is_bearable(v)


def is_id_type_sequence(v: object) -> TypeGuard[IDTypeSeq]:
    return IDTypeSeqHint.is_bearable(v)


typecheck = beartype(
    conf=BeartypeConf(
        violation_param_type=TypeError,
        violation_verbosity=BeartypeViolationVerbosity.MINIMAL,
    )
)
