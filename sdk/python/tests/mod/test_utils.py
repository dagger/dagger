import dataclasses
from typing import Annotated, List, Optional, Protocol  # noqa: UP035

import pytest
from beartype.door import TypeHint
from typing_extensions import Doc, Self

from dagger import Name
from dagger.mod._utils import (
    get_alt_name,
    get_doc,
    is_list_type,
    is_nullable,
    list_of,
    non_null,
    normalize_name,
)


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, False),
        (str | int, False),
        (str | None, True),
        (Optional[str], True),
    ],
)
def test_is_nullable(typ, expected):
    assert is_nullable(TypeHint(typ)) == expected


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, str),
        (str | None, str),
        (Optional[str], str),
        (str | int | None, str | int),
        (str | int, str | int),
    ],
)
def test_non_optional(typ, expected):
    assert non_null(TypeHint(typ)) == TypeHint(expected)


@dataclasses.dataclass
class ObjWithDoc:
    """Foo."""

    @classmethod
    def create(cls) -> Self:
        """Bar."""
        return cls()

    def with_doc(self):
        """Foo."""

    async def async_with_doc(self):
        """Foo."""


class IfaceWithDoc(Protocol):
    """Foo."""

    def with_doc(self):
        """Foo."""

    async def async_with_doc(self):
        """Foo."""


@pytest.mark.parametrize(
    "annotation",
    [
        ObjWithDoc,
        ObjWithDoc.with_doc,
        ObjWithDoc.async_with_doc,
        IfaceWithDoc,
        IfaceWithDoc.with_doc,
        IfaceWithDoc.async_with_doc,
        Annotated[str, Doc("Foo.")],
        Annotated[str | None, Doc("Foo.")],
        Annotated[str, Doc("Foo."), "Not supported"],
        Annotated[str, Doc("Bar."), Doc("Foo.")],
    ],
)
def test_get_doc(annotation):
    assert get_doc(annotation) == "Foo."


def test_get_factory_doc():
    assert get_doc(ObjWithDoc.create) == "Bar."


@dataclasses.dataclass
class ObjWithoutDoc:
    def without_doc(self): ...
    async def async_without_doc(self): ...


class IfaceWithoutDoc(Protocol):
    def without_doc(self): ...
    async def async_without_doc(self): ...


@pytest.mark.parametrize(
    "annotation",
    [
        ObjWithoutDoc,
        ObjWithoutDoc.without_doc,
        ObjWithoutDoc.async_without_doc,
        IfaceWithoutDoc,
        IfaceWithoutDoc.without_doc,
        IfaceWithoutDoc.async_without_doc,
        str,
        str | None,
        Annotated[str, "Not supported"],
        Annotated[str, Name("foo")],
    ],
)
def test_no_annotated_doc(annotation):
    assert get_doc(annotation) is None


@pytest.mark.parametrize(
    ("name", "expected"),
    [
        ("with_", "with"),
        ("__init__", "__init__"),
        ("_private_", "_private_"),
        ("mangled__", "mangled__"),
    ],
)
def test_normalize_name(name: str, expected: str):
    assert normalize_name(name) == expected


def test_get_alt_name():
    assert get_alt_name(Annotated[str, Name("foo")]) == "foo"


def test_get_last_alt_name():
    assert get_alt_name(Annotated[str, Name("foo"), Name("bar")]) == "bar"


@pytest.mark.parametrize(
    "annotation",
    [
        str,
        Annotated[str, Doc("foo")],
    ],
)
def test_no_get_alt_name(annotation):
    assert get_alt_name(annotation) is None


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, False),
        (list[str], True),
        (List[str], True),  # noqa: UP006
        (tuple[str, int], False),
        (tuple[str, ...], True),
    ],
)
def test_is_list(typ, expected):
    assert is_list_type(typ) == expected


class Foo: ...


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, None),
        (list[str], str),
        (List[str], str),  # noqa: UP006
        (list[Foo], Foo),
    ],
)
def test_list_of(typ, expected):
    assert list_of(typ) == expected
