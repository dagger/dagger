from collections import deque
from typing import NamedTuple

import pytest

from dagger.api.base import Context, InvalidQueryError, Scalar


class SomeID(Scalar):
    ...


class F(NamedTuple):
    name: str


@pytest.fixture(name="ctx")
def context(mocker):
    session = mocker.MagicMock()
    schema = mocker.MagicMock()
    selections = deque(
        [
            F("one"),
            F("two"),
            F("three"),
        ],
    )
    return Context(session, schema, selections)


def test_none(ctx: Context):
    assert ctx._get_value(None, int | None) is None


def test_optional_parent(ctx: Context):
    with pytest.raises(InvalidQueryError):
        ctx._get_value(None, int)


def test_optional_parent_with_optional_value(ctx: Context):
    r = {"one": {"two": None}}
    assert ctx._get_value(r, int | None) is None


def test_value_with_optional_type(ctx: Context):
    r = {"one": {"two": {"three": 3}}}
    assert ctx._get_value(r, int | None) == 3


def test_none_value_with_optional_type(ctx: Context):
    r = {"one": {"two": {"three": None}}}
    assert ctx._get_value(r, int | None) is None


def test_scalar(ctx: Context):
    r = {"one": {"two": {"three": "144"}}}
    actual = ctx._get_value(r, SomeID)
    assert isinstance(actual, SomeID)
    assert actual == "144"
