from collections.abc import Sequence

import pytest
from pytest_lazyfixture import lazy_fixture

import dagger
from dagger.client._core import (
    Root,
)
from dagger.client._guards import is_id_type, is_id_type_sequence, typecheck
from dagger.client.base import Scalar, Type

pytestmark = pytest.mark.filterwarnings("ignore:coroutine")


class DirectoryID(Scalar):
    ...


class FileID(Scalar):
    ...


class Client(Root):
    @typecheck
    def container(self) -> "Container":
        return Container(self._ctx)

    @typecheck
    def directory(self, id: DirectoryID | None = None) -> "Directory":
        return Directory(self._ctx)

    @typecheck
    def file(self, id: FileID) -> "File":
        return File(self._ctx)


class Container(Type):
    @typecheck
    def with_exec(self, args: Sequence[str]) -> "Container":
        return Container(self._ctx)

    @typecheck
    def with_env_variable(self, name: str, value: str) -> "Container":
        return Container(self._ctx)

    @typecheck
    def with_directory(self, path: str, directory: "Directory") -> "Container":
        return Container(self._ctx)

    @typecheck
    def with_file(self, path: str, source: "File") -> "Container":
        return Container(self._ctx)


class Directory(Type):
    @typecheck
    async def id(self) -> DirectoryID:
        return DirectoryID("dirhash")


class File(Type):
    ...


@pytest.fixture()
def client(mocker):
    return Client(mocker.MagicMock())


@pytest.fixture()
def file(client: Client):
    return client.file(FileID(""))


def test_str(client: Client):
    with pytest.raises(TypeError):
        client.container().with_env_variable("SPAM", 144)


# TODO: There's flakiness in this test
# def test_list_str(client: Client):
#     with pytest.raises(TypeError):
#         client.container().with_exec(["echo", 123])


def test_id(client: Client):
    client.directory(DirectoryID("dirid"))


def test_id_instance(client: Client):
    with pytest.raises(TypeError):
        client.directory("dirid")


def test_wrong_id_type(client: Client):
    with pytest.raises(TypeError):
        client.directory(FileID("fileid"))


def test_object(client: Client):
    client.container().with_directory("spam", client.directory())


def test_wrong_object(client: Client):
    with pytest.raises(TypeError):
        client.container().with_file("a", client.directory())


def test_no_object_id(client: Client):
    with pytest.raises(TypeError):
        client.container().with_file("a", FileID("fileid"))


@pytest.mark.anyio()
async def test_await(client: Client):
    client.directory(await client.directory().id())


# TODO: warning is not being ignored here and leaked to next async test
# -> RuntimeWarning: coroutine 'Directory.id' was never awaited
# @pytest.mark.filterwarnings("ignore:coroutine")
# @pytest.mark.anyio
# async def test_missing_await(client: Client):
#     with pytest.raises(TypeError, match=r"Did you forget to await\?"):
#         client.directory(client.directory().id())


def test_required(client: Client):
    client.file(FileID("filehash"))
    with pytest.raises(TypeError):
        client.file()


def test_input_object():
    arg = dagger.BuildArg("NAME", "value")

    assert (arg.name, arg.value) == ("NAME", "value")


def test_is_id_type(client: Client):
    assert is_id_type(client.directory())


class WithoutID:
    ...


class WithID:
    async def id(self) -> Scalar:
        return FileID("")


@pytest.mark.parametrize(
    "val",
    [
        "",
        "spam",
        True,
        WithID(),
        WithoutID(),
        DirectoryID("dir"),
        lazy_fixture("file"),
    ],
)
def test_is_not_id_type(val):
    assert not is_id_type(val)


@pytest.mark.parametrize(
    "seq",
    [list, tuple],
)
def test_is_id_type_sequence(client: Client, seq):
    val = seq(client.directory() for _ in range(3))
    assert is_id_type_sequence(val)


@pytest.mark.parametrize(
    "val",
    [
        "",
        "spam",
        ["x", "y", "z"],
        [WithID()],
        [lazy_fixture("file")],
    ],
)
def test_is_not_id_type_sequence(val):
    assert not is_id_type_sequence(val)
