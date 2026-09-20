from collections.abc import Sequence

import pytest

import dagger
from dagger.client._core import Arg, Context
from dagger.client._guards import is_id_type, is_id_type_sequence, typecheck
from dagger.client.base import Root, Scalar, Type

pytestmark = pytest.mark.filterwarnings("ignore:coroutine")


class DirectoryID(Scalar): ...


class FileID(Scalar): ...


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


class File(Type): ...


@pytest.fixture
def client(mocker):
    return Client(mocker.MagicMock())


@pytest.fixture
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


@pytest.mark.anyio
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


@pytest.mark.anyio
@pytest.mark.parametrize(
    "depth", [-1, 0, 2], ids=["direct-file", "content-block", "nested-content"]
)
async def test_llm_content_file_id_resolution(mocker, depth):
    client = dagger.Client(Context())
    file = client.file("image.png", "image bytes")
    file_id = "file-id"
    resolve_id = mocker.patch.object(dagger.File, "id", return_value=file_id)
    image = dagger.LLMContentBlockInput(
        kind=dagger.LLMContentBlockKind.IMAGE,
        file=file,
    )
    block = image

    if depth >= 0:
        for _ in range(depth):
            block = dagger.LLMContentBlockInput(
                kind=dagger.LLMContentBlockKind.TOOL_RESULT, content=[block]
            )
        llm = client.llm().with_content([block])
    else:
        llm = client.llm().with_content_file(file)

    await llm._ctx.resolve_ids()
    args = llm._ctx.selections[-1].args
    if depth >= 0:
        args = args["content"][0]
        for _ in range(depth):
            args = args["content"][0]
    assert args["file"] == file_id
    # Do not overwrite the caller's File-typed input or refetch cached IDs.
    assert image.file is file
    await llm._ctx.resolve_ids()
    resolve_id.assert_awaited_once()


@pytest.mark.anyio
async def test_resolve_ids_in_mixed_input_sequences(mocker):
    client = dagger.Client(Context())
    file = client.file("image.png", "image bytes")
    resolve_id = mocker.patch.object(dagger.File, "id", return_value="file-id")
    plain = [None, "text", 0, False, {"empty": []}]
    value = {
        "files": (file, file),
        "mixed": [file, *plain],
        "kind": dagger.LLMContentBlockKind.IMAGE,
    }
    ctx = Context().select("Query", "example", [Arg("input", value)])

    await ctx.resolve_ids()

    assert ctx.selections[-1].args == {
        "input": {
            "files": ["file-id", "file-id"],
            "mixed": ["file-id", *plain],
            "kind": "IMAGE",
        }
    }
    assert value["files"] == (file, file)
    assert resolve_id.await_count == 3


def test_is_id_type(client: Client):
    assert is_id_type(client.directory())


class WithoutID: ...


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
    ],
)
def test_is_not_id_type(val):
    assert not is_id_type(val)


def test_is_file_not_id_type(file):
    assert not is_id_type(file)


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
    ],
)
def test_is_not_id_type_sequence(val):
    assert not is_id_type_sequence(val)


def test_file_is_not_id_type_sequence(file):
    assert not is_id_type_sequence([file])
