from collections import deque
from typing import NamedTuple

import pytest

from dagger.client._core import Context, Field, InvalidQueryError
from dagger.client.base import Scalar


class SomeID(Scalar):
    ...


class F(NamedTuple):
    name: str


@pytest.fixture(name="ctx")
def context(mocker):
    connection = mocker.MagicMock()
    selections = deque(Field("T", f, {}) for f in ("one", "two", "three"))
    return Context(connection, selections)


def test_none(ctx: Context):
    assert ctx.get_value(None, int | None) is None


def test_optional_parent(ctx: Context):
    with pytest.raises(InvalidQueryError):
        ctx.get_value(None, int)


def test_optional_parent_with_optional_value(ctx: Context):
    r = {"one": {"two": None}}
    assert ctx.get_value(r, int | None) is None


def test_value_with_optional_type(ctx: Context):
    r = {"one": {"two": {"three": 3}}}
    assert ctx.get_value(r, int | None) == 3


def test_none_value_with_optional_type(ctx: Context):
    r = {"one": {"two": {"three": None}}}
    assert ctx.get_value(r, int | None) is None


def test_scalar(ctx: Context):
    r = {"one": {"two": {"three": "144"}}}
    actual = ctx.get_value(r, SomeID)
    assert isinstance(actual, SomeID)
    assert actual == "144"


def test_list(ctx: Context):
    r = {"one": {"two": ["200", "201"]}}
    actual = ctx.get_value(r, list[SomeID])
    assert actual == [SomeID("200"), SomeID("201")]
