from typing import Annotated, Optional

import pytest
from beartype.door import TypeHint
from typing_extensions import Doc, Self

from dagger import Arg, field
from dagger.mod import Module
from dagger.mod._utils import get_arg_name, get_doc, is_nullable, non_null


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


class ClassWithDocstring:
    """Foo."""

    @classmethod
    def create(cls) -> Self:
        """Bar."""
        return cls()


def func_with_docstring():
    """Foo."""


async def async_func_with_docstring():
    """Foo."""


@pytest.mark.parametrize(
    "annotation",
    [
        ClassWithDocstring,
        func_with_docstring,
        async_func_with_docstring,
        Annotated[str, Doc("Foo.")],
        Annotated[str | None, Doc("Foo.")],
        Annotated[str, Doc("Foo."), "Not supported"],
        Annotated[str, Doc("Bar."), Doc("Foo.")],
    ],
)
def test_get_doc(annotation):
    assert get_doc(annotation) == "Foo."


def test_get_factory_doc():
    assert get_doc(ClassWithDocstring.create) == "Bar."


class ClassWithoutDocstring:
    ...


def func_without_docstring():
    ...


async def async_func_without_docstring():
    ...


@pytest.mark.parametrize(
    "annotation",
    [
        ClassWithoutDocstring,
        func_without_docstring,
        async_func_without_docstring,
        str,
        str | None,
        Annotated[str, "Not supported"],
        Annotated[str, Arg("foo")],
    ],
)
def test_no_annotated_doc(annotation):
    assert get_doc(annotation) is None


def test_no_dataclass_default_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        bar: str = field()

    assert get_doc(Foo) is None


def test_get_arg_name():
    assert get_arg_name(Annotated[str, Arg("foo")]) == "foo"


def test_get_last_arg_name():
    assert get_arg_name(Annotated[str, Arg("foo"), Arg("bar")]) == "bar"


@pytest.mark.parametrize(
    "annotation",
    [
        str,
        Annotated[str, Doc("foo")],
    ],
)
def test_no_get_arg_name(annotation):
    assert get_arg_name(annotation) is None
