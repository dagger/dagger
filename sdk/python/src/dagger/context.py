# TODO: Rename this file managers.py
import contextlib
import typing

from typing_extensions import Self


class ResourceManager(contextlib.AbstractAsyncContextManager):
    def __init__(self):
        super().__init__()
        self.stack = contextlib.AsyncExitStack()

    @contextlib.asynccontextmanager
    async def get_stack(self) -> typing.AsyncIterator[contextlib.AsyncExitStack]:
        async with self.stack as stack:
            yield stack
            self.stack = stack.pop_all()

    # For type checker as inherited method isn't typed.
    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(self, *_) -> None:
        await self.stack.aclose()


class SyncResourceManager(contextlib.AbstractContextManager):
    def __init__(self):
        super().__init__()
        self.sync_stack = contextlib.ExitStack()

    @contextlib.contextmanager
    def get_sync_stack(self) -> typing.Iterator[contextlib.ExitStack]:
        with self.sync_stack as stack:
            yield stack
            self.sync_stack = stack.pop_all()

    def __exit__(self, *_) -> None:
        self.sync_stack.close()
