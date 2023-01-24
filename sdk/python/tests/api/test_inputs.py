from collections.abc import Sequence

import pytest

from dagger.api.base import Root, Scalar, Type, typecheck

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


def test_str(client: Client):
    with pytest.raises(TypeError):
        client.container().with_env_variable("SPAM", 144)


# FIXME: There's flakiness in this test
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


# FIXME: warning is not being ignored here and leaked to next async test
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
