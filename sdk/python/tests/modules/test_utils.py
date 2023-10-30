from typing import Annotated

import pytest
from typing_extensions import Doc

from dagger.mod._arguments import Arg
from dagger.mod._utils import get_arg_name, get_doc, is_optional, non_optional


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, False),
        (str | int, False),
        (str | None, True),
    ],
)
def test_is_optional(typ, expected):
    assert is_optional(typ) == expected


@pytest.mark.parametrize(
    ("typ", "expected"),
    [
        (str, str),
        (str | None, str),
        (str | int | None, str | int),
        (str | int, str | int),
    ],
)
def test_non_optional(typ, expected):
    assert non_optional(typ) == expected


class ClassWithDocstring:
    """Foo."""


def func_with_docstring():
    """Foo."""


@pytest.mark.parametrize(
    "annotation",
    [
        ClassWithDocstring,
        ClassWithDocstring(),
        func_with_docstring,
        Annotated[str, Doc("Foo.")],
        Annotated[str, Doc("Foo."), "Not supported"],
        Annotated[str, Doc("Bar."), Doc("Foo.")],
    ],
)
def test_get_doc(annotation):
    assert get_doc(annotation) == "Foo."


class ClassWithoutDocstring:
    ...


def func_without_docstring():
    ...


@pytest.mark.parametrize(
    "annotation",
    [
        ClassWithoutDocstring,
        ClassWithoutDocstring(),
        func_without_docstring,
        str,
        Annotated[str, "Not supported"],
        Annotated[str, Arg("foo")],
    ],
)
def test_no_annotated_doc(annotation):
    assert get_doc(annotation) is None


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
