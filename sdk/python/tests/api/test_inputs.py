from typing import NewType

import pytest
from pytest_lazyfixture import lazy_fixture

from dagger.api.base import Arg, Type


class Directory(Type):
    ...


class File(Type):
    ...


FileID = NewType("FileID", str)


@pytest.fixture
def directory(mocker):
    return Directory(mocker.MagicMock())


@pytest.fixture
def file(mocker):
    return File(mocker.MagicMock())


@pytest.fixture
def file_list(file):
    return [file]


@pytest.mark.parametrize(
    "value, type_",
    [
        ("abc", str),
        ("abc", str | None),
        (None, str | None),
        (None, bool | None),
        (True, bool | None),
        (False, bool | None),
        (None, None),
        (lazy_fixture("file"), File),
        (lazy_fixture("file_list"), list[File]),
        (None, list[File] | None),
        (["a", "b", "c"], list[str] | None),
        (None, list[str] | None),
        (None, list[str | None] | None),
        ([None], list[str | None] | None),
        ("abc", FileID),
        (FileID("abc"), FileID),
        ("abc", FileID | File),
        (None, FileID | File | None),
        (lazy_fixture("file"), FileID | File),
    ],
)
def test_right_type(directory: Directory, value, type_):
    directory._convert_args([Arg("egg", "egg", value, type_)])


@pytest.mark.parametrize(
    "value, type_",
    [
        ("abc", int),
        ("abc", int | None),
        ("abc", File),
        (FileID("abc"), File),
        (lazy_fixture("directory"), File),
        ([None], list[int]),
    ],
)
def test_wrong_type(directory: Directory, value, type_):
    with pytest.raises(TypeError, match=r"Wrong type .* Expected .* instead"):
        directory._convert_args([Arg("egg", "egg", value, type_)])
